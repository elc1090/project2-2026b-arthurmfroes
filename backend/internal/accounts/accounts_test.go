package accounts_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/api"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/accounts"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

func TestAccountsAndCatalogAcrossGateways(t *testing.T) {
	raw := os.Getenv("ACERVO_ACCOUNTS_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_ACCOUNTS_TEST_DATABASE_URL required for real CockroachDB tests")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/acervo_accounts_test" {
		t.Fatal("requires dedicated acervo_accounts_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adminURL := *u
	adminURL.Path = "/defaultdb"
	admin, err := database.Open(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err = admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_accounts_test"); err != nil {
		t.Fatal(err)
	}
	base, err := database.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := fmt.Sprintf("accounts_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, err := base.Exec(clean, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pool, err := database.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	peerURL := *u
	if peer := os.Getenv("ACERVO_ACCOUNTS_PEER_HOST"); peer != "" {
		peerURL.Host = peer
	}
	peer, err := database.Open(ctx, peerURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	a, b := accounts.Service{Pool: pool}, accounts.Service{Pool: peer}
	alice, err := a.Register(ctx, "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := b.Register(ctx, "bob", "other")
	if err != nil {
		t.Fatal(err)
	}
	if alice.IsAdmin || bob.IsAdmin {
		t.Fatal("registration grants admin")
	}
	var hash string
	if err = pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", alice.ID).Scan(&hash); err != nil || hash == "secret" {
		t.Fatalf("password storage: %q %v", hash, err)
	}
	if _, err = b.Login(ctx, "alice", "wrong"); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	session, err := a.Login(ctx, "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if user, err := b.Authenticate(ctx, session.Token); err != nil || user.ID != alice.ID {
		t.Fatalf("shared session: %v %v", user, err)
	}
	sum := sha256.Sum256([]byte(session.Token))
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE token_hash=$1", sum[:]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("hashed session: %d %v", count, err)
	}
	if err = b.Logout(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Authenticate(ctx, session.Token); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("revoked session: %v", err)
	}
	session, err = a.Login(ctx, "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256([]byte(session.Token))
	if _, err = pool.Exec(ctx, "UPDATE sessions SET created_at=now()-INTERVAL '2 days',expires_at=now()-INTERVAL '1 day' WHERE token_hash=$1", sum[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Authenticate(ctx, session.Token); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("expired session: %v", err)
	}
	t.Run("concurrent registration", func(t *testing.T) {
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, s := range []accounts.Service{a, b} {
			wg.Add(1)
			go func(s accounts.Service) {
				defer wg.Done()
				_, err := s.Register(ctx, "same", "password")
				results <- err
			}(s)
		}
		wg.Wait()
		close(results)
		success, conflict := 0, 0
		for err := range results {
			if err == nil {
				success++
			} else if errors.Is(err, accounts.ErrConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("success=%d conflict=%d", success, conflict)
		}
	})
	ca, cb := catalog.Service{Pool: pool}, catalog.Service{Pool: peer}
	absent := "00000000-0000-4000-8000-000000000001"
	if _, err := ca.CreateFolder(ctx, alice.ID, &absent, "child"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}

	root, err := ca.CreateFolder(ctx, alice.ID, nil, "Documentos")
	if err != nil {
		t.Fatal(err)
	}
	child, err := cb.CreateFolder(ctx, alice.ID, &root.ID, "Cadeira")
	if err != nil {
		t.Fatal(err)
	}
	listing, err := ca.List(ctx, alice.ID, &root.ID)
	if err != nil || len(listing.Folders) != 1 || listing.Folders[0].ID != child.ID {
		t.Fatalf("nested listing: %+v %v", listing, err)
	}
	if _, err = cb.List(ctx, bob.ID, &root.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("foreign listing: %v", err)
	}
	if _, err = cb.CreateFolder(ctx, bob.ID, &root.ID, "invasion"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("foreign parent: %v", err)
	}
	if listing, err = cb.List(ctx, bob.ID, nil); err != nil || len(listing.Folders) != 0 || len(listing.Files) != 0 {
		t.Fatalf("private root: %+v %v", listing, err)
	}
	t.Run("concurrent folder name", func(t *testing.T) {
		results := make(chan error, 2)
		for _, s := range []catalog.Service{ca, cb} {
			go func(s catalog.Service) { _, err := s.CreateFolder(ctx, alice.ID, nil, "duplicate"); results <- err }(s)
		}
		first, second := <-results, <-results
		if !((first == nil && errors.Is(second, catalog.ErrConflict)) || (second == nil && errors.Is(first, catalog.ErrConflict))) {
			t.Fatalf("%v / %v", first, second)
		}
	})
	t.Run("transaction guard rolls back after mutation", func(t *testing.T) {
		rejected := errors.New("node excluded")
		calls := 0
		guarded := accounts.Service{Pool: pool, Guard: func(context.Context, pgx.Tx) error {
			calls++
			if calls == 2 {
				return rejected
			}
			return nil
		}}
		if _, err := guarded.Register(ctx, "must-rollback", "password"); !errors.Is(err, rejected) {
			t.Fatalf("guard error: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE login='must-rollback'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("uncommitted account count=%d err=%v", count, err)
		}
		calls = 0
		folders := catalog.Service{Pool: pool, Guard: guarded.Guard}
		if _, err := folders.CreateFolder(ctx, alice.ID, nil, "must-rollback"); !errors.Is(err, rejected) {
			t.Fatalf("folder guard error: %v", err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM folders WHERE name='must-rollback'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("uncommitted folder count=%d err=%v", count, err)
		}
	})
	t.Run("HTTP sessions shared between servers", func(t *testing.T) {
		first := httptest.NewServer((&api.API{Accounts: a, Catalog: ca, Eligible: func(*http.Request) error { return nil }}).Handler())
		defer first.Close()
		second := httptest.NewServer((&api.API{Accounts: b, Catalog: cb, Eligible: func(*http.Request) error { return nil }}).Handler())
		defer second.Close()
		response, err := http.Post(first.URL+"/api/login", "application/json", strings.NewReader(`{"login":"alice","password":"secret"}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || len(response.Cookies()) != 1 {
			t.Fatalf("login status=%d cookies=%v", response.StatusCode, response.Cookies())
		}
		cookie := response.Cookies()[0]
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatal("session cookie attributes")
		}
		request, _ := http.NewRequest("GET", second.URL+"/api/me", nil)
		request.AddCookie(cookie)
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), alice.ID) || strings.Contains(string(body), cookie.Value) {
			t.Fatalf("shared HTTP identity: %d %s", response.StatusCode, body)
		}
		request, _ = http.NewRequest("POST", second.URL+"/api/logout", nil)
		request.AddCookie(cookie)
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 204 {
			t.Fatalf("logout=%d", response.StatusCode)
		}
		request, _ = http.NewRequest("GET", first.URL+"/api/me", nil)
		request.AddCookie(cookie)
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("revoked=%d", response.StatusCode)
		}
	})
	t.Log("shared sessions, logout, expiry, concurrent registration and private nested folders passed")
}

func TestInvalidFolderNames(t *testing.T) {
	for _, name := range []string{"", " ", ".", "..", "a/b", "a\\b", "a\x00b", "a\nb"} {
		if catalog.ValidName(name) {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"sem extensão", "relatório.bin", "unknown.example"} {
		if !catalog.ValidName(name) {
			t.Errorf("rejected %q", name)
		}
	}
}
