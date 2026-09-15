package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthDoesNotImplyEligibility(t *testing.T) {
	handler := newHandler(nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/health/live", http.StatusOK},
		{http.MethodGet, "/health/ready", http.StatusServiceUnavailable},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed},
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodPost, "/uploads", http.StatusNotFound},
		{http.MethodGet, "/health/live/unexpected", http.StatusNotFound},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status {
				t.Fatalf("status = %d; want %d", response.Code, tc.status)
			}
			if tc.path == "/health/ready" && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("readiness must not be cached")
			}
		})
	}
}

func TestRunRejectsInvalidConfigBeforeListening(t *testing.T) {
	t.Setenv("PORT", "invalid")
	if err := run(); err == nil || err.Error() != "PORT must be an integer between 1 and 65535" {
		t.Fatalf("unexpected configuration error: %v", err)
	}
}
