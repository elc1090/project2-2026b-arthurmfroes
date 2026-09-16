package faultcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRequiresInternalAuthentication(t *testing.T) {
	handler := newTestHandler(t, &recordingDriver{})
	request := httptest.NewRequest(http.MethodPost, "/v1/actions", strings.NewReader(`{"idempotency_key":"one","node_id":"node-1","component":"backend","action":"stop"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHandlerAcceptsFlatContractAndDoesNotExposeSecrets(t *testing.T) {
	handler := newTestHandler(t, &recordingDriver{})
	body := `{"idempotency_key":"one","node_id":"node-1","component":"backend","action":"stop"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/actions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer internal-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var action Action
	if err := json.Unmarshal(response.Body.Bytes(), &action); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "/v1/actions?id="+action.ID, nil)
	get.Header.Set("Authorization", "Bearer internal-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", got.Code, got.Body.String())
	}
	if bytes.Contains(got.Body.Bytes(), []byte("internal-secret")) || bytes.Contains(got.Body.Bytes(), []byte("node-1-backend")) {
		t.Fatalf("secret or provider target leaked: %s", got.Body.String())
	}
	for _, field := range []string{`"node_id":"node-1"`, `"component":"backend"`, `"action":"stop"`, `"status":`} {
		if !strings.Contains(got.Body.String(), field) {
			t.Fatalf("response missing %s: %s", field, got.Body.String())
		}
	}
}

func TestHandlerListsActionsWhenIDIsAbsent(t *testing.T) {
	handler := newTestHandler(t, &recordingDriver{})
	for _, key := range []string{"first", "second"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/actions", strings.NewReader(`{"idempotency_key":"`+key+`","node_id":"node-1","component":"sql","action":"stop"}`))
		request.Header.Set("Authorization", "Bearer internal-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("POST status = %d, body = %s", response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/actions", nil)
	request.Header.Set("Authorization", "Bearer internal-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", response.Code, response.Body.String())
	}
	var actions []Action
	if err := json.Unmarshal(response.Body.Bytes(), &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatalf("listed %d actions, want 2", len(actions))
	}
	for _, action := range actions {
		if action.Component != ComponentSQL {
			t.Fatalf("component = %q, want sql", action.Component)
		}
	}
}

func TestHandlerMapsTargetConflictAndDriverErrors(t *testing.T) {
	handler := newTestHandler(t, &recordingDriver{})
	call := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/actions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer internal-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if got := call(`{"idempotency_key":"missing","node_id":"node-9","component":"backend","action":"stop"}`); got.Code != http.StatusNotFound {
		t.Fatalf("missing target status = %d", got.Code)
	}
	first := call(`{"idempotency_key":"same","node_id":"node-1","component":"backend","action":"stop"}`)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", first.Code)
	}
	if got := call(`{"idempotency_key":"same","node_id":"node-1","component":"backend","action":"restore"}`); got.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d", got.Code)
	}

	mapping, err := NewMapping(ModeDocker, testTargets("node-1"), "fault-actuator")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(UnavailableDriver{Reason: ErrRailwayUnavailable}, mapping)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := NewHandler(service, "internal-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/actions", strings.NewReader(`{"idempotency_key":"down","node_id":"node-1","component":"backend","action":"stop"}`))
	request.Header.Set("Authorization", "Bearer internal-secret")
	response := httptest.NewRecorder()
	unavailable.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, body = %s", response.Code, response.Body.String())
	}
}

func newTestHandler(t *testing.T, driver Driver) http.Handler {
	t.Helper()
	service := newTestService(t, driver)
	handler, err := NewHandler(service, "internal-secret")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

var _ Driver = driverFunc(nil)

type driverFunc func(context.Context, Operation, ResolvedTarget) error

func (f driverFunc) Apply(ctx context.Context, operation Operation, target ResolvedTarget) error {
	return f(ctx, operation, target)
}
