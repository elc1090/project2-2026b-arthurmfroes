package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPFaultControlContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer internal-secret" {
			t.Fatal("missing internal authentication")
		}
		switch r.Method {
		case http.MethodPost:
			var input FaultRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil || !validFaultRequest(input) {
				t.Fatal("invalid request body")
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(FaultAction{ID: "action-1", NodeID: input.NodeID, Component: input.Component, Action: input.Action, Status: "requested", UpdatedAt: "2026-09-16T12:00:00Z", Results: []FaultResult{}})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]FaultAction{{ID: "action-1", NodeID: "backend-node-2", Component: FaultStorage, Action: FaultStop, Status: "stopped", UpdatedAt: "2026-09-16T12:00:01Z", Results: []FaultResult{}}})
		default:
			t.Fatalf("method=%s", r.Method)
		}
	}))
	defer server.Close()
	client, err := NewHTTPFaultControl(server.URL, "internal-secret")
	if err != nil {
		t.Fatal(err)
	}
	action, err := client.Start(t.Context(), FaultRequest{NodeID: "backend-node-2", Component: FaultStorage, Action: FaultStop})
	if err != nil || action.ID != "action-1" || action.Status != "requested" {
		t.Fatalf("action=%+v err=%v", action, err)
	}
	actions, err := client.Actions(t.Context())
	if err != nil || len(actions) != 1 || actions[0].Status != "stopped" {
		t.Fatalf("actions=%+v err=%v", actions, err)
	}
}

func TestHTTPFaultControlFailsClosed(t *testing.T) {
	for _, input := range [][2]string{{"", "token"}, {"ftp://example.test", "token"}, {"http://user@example.test", "token"}, {"http://example.test/path", "token"}, {"http://example.test", ""}} {
		if _, err := NewHTTPFaultControl(input[0], input[1]); err == nil {
			t.Fatalf("accepted origin=%q token=%q", input[0], input[1])
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "private provider failure", 503) }))
	defer server.Close()
	client, _ := NewHTTPFaultControl(server.URL, "token")
	if _, err := client.Actions(t.Context()); !errors.Is(err, ErrFaultUnavailable) {
		t.Fatalf("error=%v", err)
	}
}
