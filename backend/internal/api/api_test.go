package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailsClosedBeforeAuthentication(t *testing.T) {
	for _, gate := range []func(*http.Request) error{nil, func(*http.Request) error { return errors.New("offline") }} {
		a := API{Eligible: gate}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"login":"alice","password":"secret"}`)))
		if w.Code != 503 {
			t.Fatalf("status %d", w.Code)
		}
	}
}
func TestCrossSiteMutationRejected(t *testing.T) {
	a := API{Eligible: func(*http.Request) error { t.Fatal("cross-site request reached eligibility"); return nil }}
	r := httptest.NewRequest("POST", "http://acervo.test/api/logout", nil)
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status %d", w.Code)
	}
}
func TestInputAndMissingSession(t *testing.T) {
	a := API{Eligible: func(*http.Request) error { return nil }}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"POST", "/api/register", `{"login":"alice","password":"secret","is_admin":true}`, 400}, {"POST", "/api/login", `{} {}`, 400}, {"GET", "/api/me", "", 401}, {"GET", "/api/folders", "", 401}} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Errorf("%s %s = %d", tc.method, tc.path, w.Code)
		}
	}
}
