package control

import (
	"context"
	"encoding/json"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthoritativeHTTPLease(t *testing.T) {
	pool, ctx := testPool(t)
	store := cluster.New(pool)
	node, err := store.Register(ctx, cluster.Registration{NodeID: "control", BackendEndpoint: "http://backend", DatabaseEndpoint: "postgresql://database", StorageEndpoint: "http://storage"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{Pool: pool, Store: store, Local: node, Token: "token", StorageProbe: func(context.Context) error { return nil }, Interval: time.Millisecond, Timeout: time.Millisecond, LeaseTTL: time.Second, FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/internal/cluster", nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request(); w.Code != 503 {
		t.Fatalf("absent lease status=%d", w.Code)
	}
	if _, err := store.AcquireLease(ctx, node.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	w := request()
	if w.Code != 200 {
		t.Fatalf("live lease status=%d", w.Code)
	}
	var data struct {
		ValidFor      int64            `json:"valid_for_ms"`
		Configuration cluster.Snapshot `json:"configuration"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.ValidFor <= 0 || data.ValidFor > 5000 || len(data.Configuration.Members) != 0 {
		t.Fatalf("snapshot=%v err=%v", data, err)
	}
	waitLeaseExpired(t, ctx, pool)
	if w := request(); w.Code != 503 {
		t.Fatalf("expired lease status=%d", w.Code)
	}
}
