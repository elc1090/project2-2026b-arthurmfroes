package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testNode struct {
	controller *Controller
	server     *httptest.Server
	healthy    atomic.Bool
	node       cluster.Node
}

func TestControlHTTPAndSQL(t *testing.T) {
	pool, ctx := testPool(t)
	store := cluster.New(pool)
	nodes := make([]*testNode, 3)
	cleanup := t.Cleanup
	for i := range nodes {
		n := &testNode{}
		n.healthy.Store(true)
		n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.controller.Handler().ServeHTTP(w, r) }))
		t.Cleanup(n.server.Close)
		var err error
		n.node, err = store.Register(ctx, cluster.Registration{NodeID: fmt.Sprintf("node-%d", i), BackendEndpoint: n.server.URL, DatabaseEndpoint: "postgresql://db/acervo", StorageEndpoint: "http://storage"})
		if err != nil {
			t.Fatal(err)
		}
		n.controller, err = New(Config{Pool: pool, Store: store, Local: n.node, Token: "test-control-token", StorageProbe: func(context.Context) error {
			if !n.healthy.Load() {
				return errors.New("storage down")
			}
			return nil
		}, Interval: 50 * time.Millisecond, Timeout: 2 * time.Second, LeaseTTL: 12 * time.Second, FailureThreshold: 2})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = n
	}
	t.Run("authentication and fail closed before bootstrap", func(t *testing.T) {
		response, err := http.Get(nodes[0].server.URL + "/internal/probe")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("unauthorized status=%d", response.StatusCode)
		}
		if err := nodes[0].controller.Eligible(ctx); !errors.Is(err, cluster.ErrNoAuthority) {
			t.Fatalf("unadmitted eligibility: %v", err)
		}
	})
	if err := nodes[0].controller.Step(ctx); err != nil {
		t.Fatal(err)
	}
	assertMembers(t, ctx, store, 3)
	for _, n := range nodes {
		if err := n.controller.Eligible(ctx); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("partial storage failure removes whole node", func(t *testing.T) {
		nodes[1].healthy.Store(false)
		if err := nodes[1].controller.Eligible(ctx); !errors.Is(err, cluster.ErrNoAuthority) {
			t.Fatalf("local storage failure: %v", err)
		}
		for range 2 {
			if err := nodes[0].controller.Step(ctx); err != nil {
				t.Fatal(err)
			}
		}
		assertMembers(t, ctx, store, 2)
		if err := database.WithTx(ctx, pool, func(tx pgx.Tx) error { return cluster.Guard(ctx, tx, nodes[1].node.ID) }); !errors.Is(err, cluster.ErrNoAuthority) {
			t.Fatalf("removed transaction guard: %v", err)
		}
		var reason string
		if err := pool.QueryRow(ctx, "SELECT reason FROM cluster_nodes WHERE id=$1", nodes[1].node.ID).Scan(&reason); err != nil || reason != "storage probe failed" {
			t.Fatalf("reason=%s err=%v", reason, err)
		}
		nodes[1].healthy.Store(true)
		if err := nodes[0].controller.Step(ctx); err != nil {
			t.Fatal(err)
		}
		assertMembers(t, ctx, store, 3)
	})
	t.Run("total HTTP failure and recovery", func(t *testing.T) {
		n := nodes[2]
		address := n.server.Listener.Addr().String()
		n.server.Close()
		for range 2 {
			if err := nodes[0].controller.Step(ctx); err != nil {
				t.Fatal(err)
			}
		}
		assertMembers(t, ctx, store, 2)
		replacement := httptest.NewUnstartedServer(n.controller.Handler())
		replacement.Listener.Close()
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		replacement.Listener = listener
		replacement.Start()
		cleanup(replacement.Close)
		n.server = replacement
		if err := nodes[0].controller.Step(ctx); err != nil {
			t.Fatal(err)
		}
		assertMembers(t, ctx, store, 3)
	})
	t.Run("manager failure successor and all unavailable recovery", func(t *testing.T) {
		before := nodes[0].controller.lease.Term
		nodes[0].healthy.Store(false)
		if err := nodes[0].controller.Step(ctx); !errors.Is(err, cluster.ErrNoAuthority) {
			t.Fatal(err)
		}
		waitLeaseExpired(t, ctx, pool)
		if err := database.WithTx(ctx, pool, func(tx pgx.Tx) error { return cluster.Guard(ctx, tx, nodes[1].node.ID) }); !errors.Is(err, cluster.ErrNoAuthority) {
			t.Fatalf("expired guard: %v", err)
		}
		for range 2 {
			if err := nodes[1].controller.Step(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if nodes[1].controller.lease.Term <= before {
			t.Fatal("manager did not advance term")
		}
		assertMembers(t, ctx, store, 2)
		// Exercise the persisted all-unavailable state after a complete outage.
		for _, n := range nodes {
			if _, err := store.Exclude(ctx, nodes[1].controller.lease, n.node.ID, "outage fixture"); err != nil {
				t.Fatal(err)
			}
		}
		assertMembers(t, ctx, store, 0)
		for _, n := range nodes {
			n.healthy.Store(true)
		}
		waitLeaseExpired(t, ctx, pool)
		if err := nodes[2].controller.Step(ctx); err != nil {
			t.Fatal(err)
		}
		assertMembers(t, ctx, store, 3)
		t.Logf("manager terms progressed to %d; all unavailable recovered", nodes[2].controller.lease.Term)
	})
	t.Run("real SQL row required", func(t *testing.T) {
		// A valid connection and SELECT 1 still work, but the shared control row is absent.
		// Use a separate empty schema, leaving the live fixture untouched.
		raw := pool.Config().ConnString()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", "pg_catalog")
		u.RawQuery = q.Encode()
		unavailable, err := database.Open(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer unavailable.Close()
		cfg := nodes[0].controller.cfg
		cfg.Pool = unavailable
		c, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if c.health(ctx).SQL {
			t.Fatal("health succeeded without shared control table")
		}
	})
}

func TestSlowRecoveryDoesNotBlockManager(t *testing.T) {
	pool, ctx := testPool(t)
	store := cluster.New(pool)
	n := &testNode{}
	n.healthy.Store(true)
	n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.controller.Handler().ServeHTTP(w, r) }))
	defer n.server.Close()
	var err error
	n.node, err = store.Register(ctx, cluster.Registration{NodeID: "manager", BackendEndpoint: n.server.URL, DatabaseEndpoint: "postgresql://db/acervo", StorageEndpoint: "http://storage"})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	n.controller, err = New(Config{Pool: pool, Store: store, Local: n.node, Token: "token", StorageProbe: func(context.Context) error { return nil }, Interval: 50 * time.Millisecond, Timeout: 2 * time.Second, LeaseTTL: 12 * time.Second, FailureThreshold: 1, Sync: func(ctx context.Context, _ cluster.Node, _ cluster.RecoveryPlan) error {
		close(started)
		defer close(finished)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Empty plan isn't relevant to SQL admission here: directly exercise the worker
	// scheduler so a held callback cannot monopolize Step's manager mutex.
	plan := cluster.RecoveryPlan{Node: n.node}
	if n.controller.syncComplete(ctx, plan) {
		t.Fatal("worker completed prematurely")
	}
	<-started
	if err := n.controller.Step(ctx); err != nil {
		t.Fatal(err)
	}
	term := n.controller.lease.Term
	if err := n.controller.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if n.controller.lease.Term != term {
		t.Fatal("slow recovery caused term loss")
	}
	// Both passes returned while the recovery callback is still blocked.
	// The unchanged term above also proves renewal did not wait for recovery.
	select {
	case <-finished:
		t.Fatal("recovery ended before release")
	default:
	}
	close(release)
	<-finished
	if !n.controller.syncComplete(ctx, plan) {
		t.Fatal("completed worker not collected")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- n.controller.Run(runCtx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSilentManagerFailureWaitsForLeaseExpiry(t *testing.T) {
	pool, ctx := testPool(t)
	store := cluster.New(pool)
	nodes := make([]*testNode, 2)
	for i := range nodes {
		n := &testNode{}
		n.healthy.Store(true)
		n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.controller.Handler().ServeHTTP(w, r) }))
		var err error
		n.node, err = store.Register(ctx, cluster.Registration{NodeID: fmt.Sprintf("silent-node-%d", i), BackendEndpoint: n.server.URL, DatabaseEndpoint: "postgresql://db/acervo", StorageEndpoint: "http://storage"})
		if err != nil {
			t.Fatal(err)
		}
		n.controller, err = New(Config{Pool: pool, Store: store, Local: n.node, Token: "test-control-token", StorageProbe: func(context.Context) error { return nil }, Interval: 10 * time.Millisecond, Timeout: 500 * time.Millisecond, LeaseTTL: 2 * time.Second, FailureThreshold: 1})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = n
	}
	defer nodes[1].server.Close()
	if err := nodes[0].controller.Step(ctx); err != nil {
		t.Fatal(err)
	}
	before := nodes[0].controller.lease
	if before.HolderID != nodes[0].node.ID {
		t.Fatalf("holder=%s", before.HolderID)
	}
	// Abrupt disappearance: the manager cannot run Step and therefore cannot
	// publish a farewell, release its lease, or change membership.
	nodes[0].server.Close()
	if err := nodes[1].controller.Step(ctx); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatalf("successor acquired live lease: %v", err)
	}
	var holder string
	var term int64
	if err := pool.QueryRow(ctx, "SELECT holder_id::STRING,term FROM manager_lease WHERE singleton=true").Scan(&holder, &term); err != nil || holder != nodes[0].node.ID || term != before.Term {
		t.Fatalf("lease changed before expiry: holder=%s term=%d err=%v", holder, term, err)
	}
	waitLeaseExpired(t, ctx, pool)
	if err := nodes[1].controller.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if nodes[1].controller.lease.HolderID != nodes[1].node.ID || nodes[1].controller.lease.Term <= before.Term {
		t.Fatalf("successor lease=%+v previous=%+v", nodes[1].controller.lease, before)
	}
	assertMembers(t, ctx, store, 1)
}

func waitLeaseExpired(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for {
		var expired bool
		if err := pool.QueryRow(ctx, "SELECT expires_at<=clock_timestamp() FROM manager_lease WHERE singleton=true").Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func assertMembers(t *testing.T, ctx context.Context, s *cluster.Store, want int) {
	t.Helper()
	snap, err := s.Snapshot(ctx)
	if err != nil || len(snap.Members) != want {
		t.Fatalf("members=%d want=%d err=%v", len(snap.Members), want, err)
	}
}
func testPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	raw := os.Getenv("ACERVO_CONTROL_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_CONTROL_TEST_DATABASE_URL required for real HTTP/Cockroach tests")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasSuffix(u.Path, "/acervo_control_test") {
		t.Fatal("use acervo_control_test database")
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
	if _, err := admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_control_test"); err != nil {
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
