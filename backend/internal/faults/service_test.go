package faults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/control"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFaultModesAndManagerDetection(t *testing.T) {
	pool, ctx := faultPool(t)
	faults := New(pool, true)
	store := cluster.New(pool)
	nodes := make([]cluster.Node, 2)
	controllers := make([]*control.Controller, 2)
	servers := make([]*httptest.Server, 2)
	for i := range nodes {
		index := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { controllers[index].Handler().ServeHTTP(w, r) }))
		defer servers[i].Close()
		var err error
		nodes[i], err = store.Register(ctx, cluster.Registration{NodeID: fmt.Sprintf("node-%d", i), BackendEndpoint: servers[i].URL, DatabaseEndpoint: "postgresql://db/acervo", StorageEndpoint: "http://storage"})
		if err != nil {
			t.Fatal(err)
		}
		controllers[i], err = control.New(control.Config{Pool: pool, Store: store, Local: nodes[i], Token: "secret-token", StorageProbe: func(context.Context) error { return nil }, LocalGate: func(ctx context.Context, component string) error {
			return faults.Check(ctx, nodes[index].ID, component)
		}, Interval: 50 * time.Millisecond, Timeout: time.Second, LeaseTTL: 10 * time.Second, FailureThreshold: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := controllers[0].Step(ctx); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Backend, SQL, Storage, Control, Total} {
		t.Run(string(mode), func(t *testing.T) {
			if err := faults.Set(ctx, nodes[1].ID, mode); err != nil {
				t.Fatal(err)
			}
			if err := faults.Set(ctx, nodes[1].ID, mode); err != nil {
				t.Fatal(err)
			} // idempotent audit
			current, err := faults.Current(ctx, nodes[1].ID)
			if err != nil || current != mode {
				t.Fatalf("mode=%s err=%v", current, err)
			}
			for _, component := range []Mode{Backend, SQL, Storage, Control} {
				err := faults.Check(ctx, nodes[1].ID, string(component))
				wantFailure := mode == Total || component == mode
				if errors.Is(err, ErrInjected) != wantFailure {
					t.Fatalf("mode=%s component=%s err=%v", mode, component, err)
				}
			}
			var state string
			if err := pool.QueryRow(ctx, "SELECT state FROM cluster_nodes WHERE id=$1", nodes[1].ID).Scan(&state); err != nil || state != "ready" {
				t.Fatalf("flag changed membership: %s %v", state, err)
			}
			if err := controllers[1].Eligible(ctx); !errors.Is(err, cluster.ErrNoAuthority) {
				t.Fatalf("eligible with failure: %v", err)
			}
			if err := controllers[0].Step(ctx); err != nil {
				t.Fatal(err)
			}
			var healthJSON []byte
			var observed time.Time
			if err := pool.QueryRow(ctx, "SELECT state,health,observed_at FROM cluster_nodes WHERE id=$1", nodes[1].ID).Scan(&state, &healthJSON, &observed); err != nil || state != "unavailable" || observed.IsZero() {
				t.Fatalf("detected state=%s time=%v err=%v", state, observed, err)
			}
			var health map[string]*bool
			if err := json.Unmarshal(healthJSON, &health); err != nil {
				t.Fatal(err)
			}
			for _, component := range []Mode{Backend, SQL, Storage, Control} {
				want := mode != Total && mode != component
				if health[string(component)] == nil || *health[string(component)] != want {
					t.Fatalf("component=%s observed=%s", component, healthJSON)
				}
			}
			if err := faults.Set(ctx, nodes[1].ID, None); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, "SELECT state FROM cluster_nodes WHERE id=$1", nodes[1].ID).Scan(&state); err != nil || state != "unavailable" {
				t.Fatal("restore bypassed admission")
			}
			if err := controllers[0].Step(ctx); err != nil {
				t.Fatal(err)
			}
			if err := controllers[1].Eligible(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
	var events int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM cluster_events WHERE kind='fault_changed'").Scan(&events); err != nil || events != 10 {
		t.Fatalf("audit events=%d err=%v", events, err)
	}
	if err := faults.Set(ctx, nodes[1].ID, Total); err != nil {
		t.Fatal(err)
	}
	disabled := New(pool, false)
	if mode, err := disabled.Current(ctx, nodes[1].ID); err != nil || mode != None {
		t.Fatalf("disabled mode=%s err=%v", mode, err)
	}
	if err := disabled.Check(ctx, nodes[1].ID, "backend"); err != nil {
		t.Fatal(err)
	}
	if err := disabled.Set(ctx, nodes[1].ID, None); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled setter=%v", err)
	}
	if err := faults.Set(ctx, nodes[1].ID, Mode("invalid")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := faults.Set(ctx, nodes[1].ID, None); err != nil {
		t.Fatal(err)
	}
	servers[1].Close()
	if err := controllers[0].Step(ctx); err != nil {
		t.Fatal(err)
	}
	var healthJSON []byte
	if err := pool.QueryRow(ctx, "SELECT health FROM cluster_nodes WHERE id=$1", nodes[1].ID).Scan(&healthJSON); err != nil {
		t.Fatal(err)
	}
	var observed cluster.Observation
	if err := json.Unmarshal(healthJSON, &observed); err != nil || observed.SQL != nil || observed.Storage != nil || observed.Backend == nil || *observed.Backend {
		t.Fatalf("offline observation=%s err=%v", healthJSON, err)
	}
	if err := faults.Set(ctx, nodes[0].ID, Total); err != nil {
		t.Fatal(err)
	}
	if err := controllers[0].Step(ctx); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatalf("failed manager kept running: %v", err)
	}
	// Authenticated restoration invokes Set directly through a dedicated control
	// route. Neither Set nor Check creates users, sessions, or published files.
	if err := faults.Set(ctx, nodes[0].ID, None); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM users)+(SELECT count(*) FROM sessions)+(SELECT count(*) FROM files)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("fault service created application data: %d %v", count, err)
	}
	t.Log("all component gates, whole-node exclusion/recovery, audit idempotency, disabled mode, unknown offline components and manager cessation passed")
}

func faultPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	raw := os.Getenv("ACERVO_FAULTS_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_FAULTS_TEST_DATABASE_URL required for SQL/HTTP fault integration")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/acervo_faults_test" {
		t.Fatal("use acervo_faults_test database")
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
	if _, err := admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_faults_test"); err != nil {
		t.Fatal(err)
	}
	base, err := database.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
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
	q.Set("pool_max_conns", "16")
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
