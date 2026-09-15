package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
)

func TestBlockedAdmissionDoesNotStopFailureDetection(t *testing.T) {
	pool, ctx := testPool(t)
	store := cluster.New(pool)
	nodes := make([]cluster.Node, 3)
	for index := range nodes {
		health := Health{Backend: true, Control: true, SQL: true, Storage: index != 2}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer conflict-test-token" {
				w.WriteHeader(401)
				return
			}
			if !health.Healthy() {
				w.WriteHeader(503)
			}
			_ = json.NewEncoder(w).Encode(health)
		}))
		t.Cleanup(server.Close)
		var err error
		nodes[index], err = store.Register(ctx, cluster.Registration{NodeID: fmt.Sprintf("conflict-node-%d", index), BackendEndpoint: server.URL, DatabaseEndpoint: "postgresql://db/acervo", StorageEndpoint: "http://storage"})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Local and failing nodes are already admitted. The healthy joining node is
	// blocked by its infrastructure lifecycle, independently of its probe result.
	for _, index := range []int{0, 2} {
		if _, err := pool.Exec(ctx, "UPDATE cluster_nodes SET state='ready' WHERE id=$1", nodes[index].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO cluster_membership(node_id) VALUES($1)", nodes[index].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO node_infrastructure(node_id,identity,blocked) VALUES($1,'{}',true)`, nodes[1].ID); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Config{Pool: pool, Store: store, Local: nodes[0], Token: "conflict-test-token", StorageProbe: func(context.Context) error { return nil }, Interval: 50 * time.Millisecond, Timeout: 2 * time.Second, LeaseTTL: 12 * time.Second, FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	// All results are buffered before Step processes them. This must succeed
	// regardless of whether the blocked or failing node's response arrived first.
	if err := controller.Step(ctx); err != nil {
		t.Fatalf("blocked admission aborted manager pass: %v", err)
	}
	var state, reason string
	var observed, member bool
	if err := pool.QueryRow(ctx, `SELECT state,reason,observed_at IS NOT NULL,EXISTS(SELECT 1 FROM cluster_membership m WHERE m.node_id=cluster_nodes.id) FROM cluster_nodes WHERE id=$1`, nodes[2].ID).Scan(&state, &reason, &observed, &member); err != nil {
		t.Fatal(err)
	}
	if state != "unavailable" || reason != "storage probe failed" || !observed || member {
		t.Fatalf("failed node was not observed/excluded: state=%s reason=%s observed=%v member=%v", state, reason, observed, member)
	}
	if err := pool.QueryRow(ctx, `SELECT state,observed_at IS NOT NULL,EXISTS(SELECT 1 FROM cluster_membership m WHERE m.node_id=cluster_nodes.id) FROM cluster_nodes WHERE id=$1`, nodes[1].ID).Scan(&state, &observed, &member); err != nil {
		t.Fatal(err)
	}
	if state != "joining" || !observed || member {
		t.Fatalf("blocked node admitted or skipped observation: state=%s observed=%v member=%v", state, observed, member)
	}
}
