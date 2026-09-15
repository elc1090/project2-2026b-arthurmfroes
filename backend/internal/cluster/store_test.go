package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func registration(name string) Registration {
	return Registration{name, "https://" + name + ".example.test", "postgresql://db.example.test:26257/acervo", "http://storage.example.test:9000"}
}
func TestRegistrationValidation(t *testing.T) {
	if err := validate(registration("node-1")); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Registration{
		{" ", "http://backend", "postgres://db", "http://storage"},
		{"node", "ftp://backend", "postgres://db", "http://storage"},
		{"node", "http://backend", "postgres://db:65536", "http://storage"},
		{"node", "http://backend", "postgres://db", "http://user:secret@storage"},
		{"node", "http://backend?token=secret", "postgres://db", "http://storage"},
	} {
		err := validate(r)
		if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid validation: %v", err)
		}
	}
}
func TestCockroachCluster(t *testing.T) {
	pool, ctx := integrationPool(t)
	store := New(pool)
	nodes := make([]Node, 4)
	for i := range nodes {
		var err error
		nodes[i], err = store.Register(ctx, registration(fmt.Sprintf("node-%d", i+1)))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Run("joining and idempotent registration", func(t *testing.T) {
		n, err := store.Register(ctx, nodes[0].Registration)
		if err != nil || n != nodes[0] || n.State != "joining" {
			t.Fatalf("node=%v err=%v", n, err)
		}
		changed := nodes[0].Registration
		changed.BackendEndpoint = "https://different.example.test"
		if _, err := store.Register(ctx, changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate err=%v", err)
		}
		snap, err := store.Snapshot(ctx)
		if err != nil || snap.Version != 0 || len(snap.Members) != 0 {
			t.Fatalf("joining eligible: %v %v", snap, err)
		}
	})
	var lease Lease
	t.Run("one winner and renewal", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan struct {
			lease Lease
			err   error
		}, 2)
		for _, node := range nodes[:2] {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				l, err := store.AcquireLease(ctx, id, 5*time.Second)
				results <- struct {
					lease Lease
					err   error
				}{l, err}
			}(node.ID)
		}
		wg.Wait()
		close(results)
		wins := 0
		for result := range results {
			if result.err == nil {
				wins++
				lease = result.lease
			} else if !errors.Is(result.err, ErrNoAuthority) {
				t.Fatal(result.err)
			}
		}
		if wins != 1 || lease.Term != 1 {
			t.Fatalf("wins=%d lease=%v", wins, lease)
		}
		renewed, err := store.RenewLease(ctx, lease, 5*time.Second)
		if err != nil || renewed.Term != lease.Term || !renewed.ExpiresAt.After(lease.ExpiresAt) {
			t.Fatalf("renewal=%v err=%v", renewed, err)
		}
		lease = renewed
	})
	if t.Failed() {
		return
	}
	t.Run("two configuration changes serialize", func(t *testing.T) {
		// Fixture for an already-admitted node. Production admission is deliberately
		// absent until its publication barrier and copy checks are implemented.
		if err := database.WithTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "UPDATE cluster_nodes SET state='ready' WHERE id=$1", nodes[2].ID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO cluster_membership(node_id) VALUES($1)", nodes[2].ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		before, err := store.Snapshot(ctx)
		if err != nil || len(before.Members) != 1 || before.Members[0].ID != nodes[2].ID {
			t.Fatalf("member snapshot=%v err=%v", before, err)
		}

		var wg sync.WaitGroup
		versions := make(chan int64, 2)
		errs := make(chan error, 2)
		for _, node := range nodes[2:] {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				v, err := store.Exclude(ctx, lease, id, "component unavailable")
				versions <- v
				errs <- err
			}(node.ID)
		}
		wg.Wait()
		close(versions)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		got := []int64{}
		for v := range versions {
			got = append(got, v)
		}
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		if got[0] != 1 || got[1] != 2 {
			t.Fatalf("versions=%v", got)
		}
		same, err := store.Exclude(ctx, lease, nodes[2].ID, "component unavailable")
		if err != nil || same != 2 {
			t.Fatalf("idempotent version=%d err=%v", same, err)
		}
		snap, err := store.Snapshot(ctx)
		if err != nil || snap.Version != 2 || snap.PublicationGeneration != 0 || len(snap.Members) != 0 {
			t.Fatalf("snapshot=%v err=%v", snap, err)
		}
		t.Logf("concurrent configuration versions: %v", got)
	})
	t.Run("expired holder fenced after lock wait and takeover", func(t *testing.T) {
		short, err := store.RenewLease(ctx, lease, 250*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		blocker, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(context.Background())
		if err := lockLease(ctx, blocker); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := store.Exclude(ctx, short, nodes[1].ID, "stale request"); result <- err }()
		for {
			var expired bool
			if err := pool.QueryRow(ctx, "SELECT clock_timestamp()>$1::TIMESTAMPTZ", short.ExpiresAt).Scan(&expired); err != nil {
				t.Fatal(err)
			}
			if expired {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, ErrNoAuthority) {
			t.Fatalf("expired mutation: %v", err)
		}
		successorID := nodes[0].ID
		if successorID == lease.HolderID {
			successorID = nodes[1].ID
		}
		successor, err := store.AcquireLease(ctx, successorID, 5*time.Second)
		if err != nil || successor.Term != lease.Term+1 {
			t.Fatalf("successor=%v err=%v", successor, err)
		}
		if _, err := store.RenewLease(ctx, lease, time.Second); !errors.Is(err, ErrNoAuthority) {
			t.Fatalf("old renewal=%v", err)
		}
		if _, err := store.Exclude(ctx, lease, nodes[0].ID, "stale term"); !errors.Is(err, ErrNoAuthority) {
			t.Fatalf("old mutation=%v", err)
		}
		snap, err := store.Snapshot(ctx)
		if err != nil || snap.Version != 2 {
			t.Fatalf("stale config: %v %v", snap, err)
		}
		t.Logf("takeover term %d -> %d; old renew/mutation rejected", lease.Term, successor.Term)
	})
}
func integrationPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	raw := os.Getenv("ACERVO_CLUSTER_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_CLUSTER_TEST_DATABASE_URL is required for real Cockroach tests")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/acervo_cluster_test" {
		t.Fatal("use dedicated acervo_cluster_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	adminURL := *u
	adminURL.Path = "/defaultdb"
	admin, err := database.Open(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_cluster_test"); err != nil {
		t.Fatal(err)
	}
	base, err := database.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer base.Close()
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := base.Exec(clean, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("pool_max_conns", "6")
	u.RawQuery = q.Encode()
	pool, err := database.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, ctx
}
