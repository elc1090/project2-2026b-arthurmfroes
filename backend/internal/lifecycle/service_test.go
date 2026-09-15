package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeAdapter struct {
	block, removed, matched bool
	sqlCalls, storageCalls  int
	lost                    func()
}

func (f *fakeAdapter) Preflight(context.Context, string, Request) error {
	if f.block {
		return ErrBlocked
	}
	return nil
}
func (f *fakeAdapter) SQLRemoved(context.Context, Identity) (bool, error) { return f.removed, nil }
func (f *fakeAdapter) RemoveSQL(context.Context, Identity) error {
	f.sqlCalls++
	f.removed = true
	if f.lost != nil {
		f.lost()
		f.lost = nil
		return context.DeadlineExceeded
	}
	return nil
}
func (f *fakeAdapter) StorageMatches(context.Context, string, Request) (bool, error) {
	return f.matched, nil
}
func (f *fakeAdapter) ChangeStorage(context.Context, string, Request) error {
	f.storageCalls++
	f.matched = true
	return nil
}
func setup(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	raw := os.Getenv("ACERVO_LIFECYCLE_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_LIFECYCLE_TEST_DATABASE_URL required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/acervo_lifecycle_test" {
		t.Fatal("dedicated acervo_lifecycle_test required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	adminURL := *u
	adminURL.Path = "/defaultdb"
	admin, err := database.Open(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err = admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_lifecycle_test"); err != nil {
		t.Fatal(err)
	}
	base, err := database.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("lifecycle_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer base.Close()
		_, err := base.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if err != nil {
			t.Error(err)
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pool, err := database.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}
func reg(name string) cluster.Registration {
	return cluster.Registration{NodeID: name, BackendEndpoint: "http://" + name + ":8080", DatabaseEndpoint: "postgresql://" + name + ":26257/db", StorageEndpoint: "http://" + name + ":9000"}
}
func request(name string) Request {
	return Request{Identity: Identity{ClusterID: "cluster", SQLNodeID: 4, DeploymentID: name, SiteName: name, StorageEndpoint: reg(name).StorageEndpoint, CredentialProfile: name}, Peers: []Identity{{ClusterID: "cluster", SQLNodeID: 1, DeploymentID: "peer", SiteName: "peer", StorageEndpoint: "http://peer:9000", CredentialProfile: "peer"}}}
}
func TestDurableLifecycle(t *testing.T) {
	ctx, pool := setup(t)
	store := cluster.New(pool)
	manager, err := store.Register(ctx, reg("manager"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Register(ctx, reg("other"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireLease(ctx, manager.ID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []cluster.Node{manager, other} {
		if err = store.StartSync(ctx, lease, n.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Admit(ctx, lease, n.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeAdapter{}
	svc := &Service{Pool: pool, Adapter: fake}
	join, err := svc.BeginJoin(ctx, lease, reg("fourth"), request("fourth"), "join-key")
	if err != nil {
		t.Fatal(err)
	}
	same, err := svc.BeginJoin(ctx, lease, reg("fourth"), request("fourth"), "join-key")
	if err != nil || same.ID != join.ID {
		t.Fatalf("idempotence %v %v", same, err)
	}
	if _, err = svc.BeginJoin(ctx, lease, reg("fifth"), request("fifth"), "second-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("parallel topology %v", err)
	}
	if err = store.StartSync(ctx, lease, join.NodeID); !errors.Is(err, cluster.ErrConflict) {
		t.Fatalf("premature sync %v", err)
	}
	if _, err = store.AcquireLease(ctx, join.NodeID, time.Second); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatalf("blocked election %v", err)
	}
	if err = svc.Step(ctx, lease, join.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(ctx, lease, join.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.StartSync(ctx, lease, join.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Admit(ctx, lease, join.NodeID, 0); err != nil {
		t.Fatal(err)
	}
	req := request("manager")
	fake.block = true
	if _, err = svc.BeginRetire(ctx, lease, manager.ID, req, "initially-blocked"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("initial preflight %v", err)
	}
	if _, err = svc.Pending(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("blocked preflight held slot %v", err)
	}
	fake.block = false
	cancellable, err := svc.BeginRetire(ctx, lease, manager.ID, req, "cancel-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.CancelRetirement(ctx, lease, cancellable.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Pending(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cancel held slot %v", err)
	}
	retire, err := svc.BeginRetire(ctx, lease, manager.ID, req, "retire-key")
	if err != nil {
		t.Fatal(err)
	}
	fake.block = true
	if err = svc.Step(ctx, lease, retire.ID); !errors.Is(err, ErrBlocked) {
		t.Fatalf("preflight %v", err)
	}
	snap, _ := store.Snapshot(ctx)
	if len(snap.Members) != 3 {
		t.Fatal("preflight excluded node")
	}
	fake.block = false
	fake.matched = false
	if err = svc.Step(ctx, lease, retire.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RenewLease(ctx, lease, 30*time.Second); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatalf("retiring manager renew %v", err)
	}
	if err = svc.Step(ctx, lease, retire.ID); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatalf("retiring manager mutation %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE manager_lease SET expires_at=clock_timestamp()-INTERVAL '1 second'`); err != nil {
		t.Fatal(err)
	}
	next, err := store.AcquireLease(ctx, other.ID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fake.lost = func() {
		if _, err := pool.Exec(ctx, `UPDATE manager_lease SET expires_at=clock_timestamp()-INTERVAL '1 second'`); err != nil {
			t.Fatal(err)
		}
	}
	if err = svc.Step(ctx, next, retire.ID); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatalf("lost authority %v", err)
	}
	final, err := store.AcquireLease(ctx, join.NodeID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(ctx, final, retire.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.CancelRetirement(ctx, final, retire.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel after remote start %v", err)
	}
	if fake.sqlCalls != 1 {
		t.Fatal("repeated already accepted SQL operation")
	}
	if err = svc.Step(ctx, final, retire.ID); err != nil {
		t.Fatal(err)
	}
	o, err := svc.Get(ctx, retire.ID)
	if err != nil || o.Stage != "complete" {
		t.Fatalf("completion %+v %v", o, err)
	}
	if err = store.StartSync(ctx, final, manager.ID); !errors.Is(err, cluster.ErrConflict) {
		t.Fatalf("retired readmission %v", err)
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT state FROM cluster_nodes WHERE id=$1`, manager.ID).Scan(&state); err != nil || state != "removed" {
		t.Fatalf("final state %s %v", state, err)
	}
	t.Logf("join fenced and admitted; preflight preserved 3 members; manager handoff %d→%d→%d; ambiguous SQL reconciled without duplicate call", lease.Term, next.Term, final.Term)
}
