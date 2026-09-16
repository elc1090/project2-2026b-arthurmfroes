package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/accounts"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeleteFileHTTPAuthorizationIdempotencyAndNameReuse(t *testing.T) {
	ctx, pool, handler, ownerCookie, otherCookie, owner, fileID, operationID := deleteAPIFixture(t)
	request := func(cookie *http.Cookie, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/api/files/"+id, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if response := request(nil, fileID); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	foreign := request(otherCookie, fileID)
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), "published.bin") || strings.Contains(foreign.Body.String(), operationID) {
		t.Fatalf("foreign response = %d %s", foreign.Code, foreign.Body.String())
	}
	var files int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM files WHERE id=$1", fileID).Scan(&files); err != nil || files != 1 {
		t.Fatalf("foreign deletion changed files=%d err=%v", files, err)
	}
	if response := request(ownerCookie, "not-a-uuid"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d body=%s", response.Code, response.Body.String())
	}
	first := request(ownerCookie, fileID)
	if first.Code != http.StatusNoContent || first.Body.Len() != 0 {
		t.Fatalf("first deletion = %d %q", first.Code, first.Body.String())
	}
	var generation int64
	var deletedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT c.publication_generation,d.deleted_at
 FROM cluster_configuration c CROSS JOIN file_deletions d WHERE c.singleton AND d.file_id=$1`, fileID).Scan(&generation, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if generation != 1 {
		t.Fatalf("publication generation = %d, want 1", generation)
	}
	// This is the lost-response case: the client has no first response state and
	// repeats the same request. It must observe success without a second mutation.
	second := request(ownerCookie, fileID)
	if second.Code != http.StatusNoContent || second.Body.Len() != 0 {
		t.Fatalf("repeated deletion = %d %q", second.Code, second.Body.String())
	}
	var repeatedGeneration int64
	var repeatedDeletedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT c.publication_generation,d.deleted_at
 FROM cluster_configuration c CROSS JOIN file_deletions d WHERE c.singleton AND d.file_id=$1`, fileID).Scan(&repeatedGeneration, &repeatedDeletedAt); err != nil {
		t.Fatal(err)
	}
	if repeatedGeneration != generation || !repeatedDeletedAt.Equal(deletedAt) {
		t.Fatalf("repeat changed generation/time: %d %s -> %d %s", generation, deletedAt, repeatedGeneration, repeatedDeletedAt)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM files WHERE id=$1", fileID).Scan(&files); err != nil || files != 0 {
		t.Fatalf("active file count=%d err=%v", files, err)
	}
	var replacementOperation, replacementFile string
	if err := pool.QueryRow(ctx, `INSERT INTO upload_operations(owner_id,name,idempotency_key,size_bytes,sha256,manifest,part_count,status,phase)
 VALUES($1,'published.bin',gen_random_uuid(),0,$2,'[]',0,'available','complete') RETURNING id::STRING`, owner, make([]byte, 32)).Scan(&replacementOperation); err != nil {
		t.Fatal("deleted name was not reusable", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO files(operation_id,owner_id,name,configuration_version)
 VALUES($1,$2,'published.bin',1) RETURNING id::STRING`, replacementOperation, owner).Scan(&replacementFile); err != nil || replacementFile == "" {
		t.Fatal("deleted name was not reusable", err)
	}
}

func deleteAPIFixture(t *testing.T) (context.Context, *pgxpool.Pool, http.Handler, *http.Cookie, *http.Cookie, string, string, string) {
	t.Helper()
	raw := os.Getenv("ACERVO_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_TEST_DATABASE_URL is required for deletion SQL/API tests")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/acervo_database_test" {
		t.Fatal("deletion tests require the dedicated acervo_database_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	adminURL := *u
	adminURL.Path = "/defaultdb"
	admin, err := database.Open(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if _, err = admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_database_test"); err != nil {
		t.Fatal(err)
	}
	base, err := database.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(base.Close)
	schema := fmt.Sprintf("delete_api_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = base.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	})
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	pool, err := database.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var owner, other string
	if err = pool.QueryRow(ctx, "INSERT INTO users(login,password_hash) VALUES('delete-owner','hash') RETURNING id::STRING").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "INSERT INTO users(login,password_hash) VALUES('delete-other','hash') RETURNING id::STRING").Scan(&other); err != nil {
		t.Fatal(err)
	}
	makeCookie := func(user string, fill byte) *http.Cookie {
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
		hash := sha256.Sum256([]byte(token))
		if _, err := pool.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+INTERVAL '1 hour')", hash[:], user); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: CookieName, Value: token}
	}
	ownerCookie := makeCookie(owner, 1)
	otherCookie := makeCookie(other, 2)
	var operationID, fileID string
	if err = pool.QueryRow(ctx, `INSERT INTO upload_operations(owner_id,name,idempotency_key,size_bytes,sha256,manifest,part_count,status,phase)
 VALUES($1,'published.bin',gen_random_uuid(),0,$2,'[]',0,'available','complete') RETURNING id::STRING`, owner, make([]byte, 32)).Scan(&operationID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO files(operation_id,owner_id,name,configuration_version)
 VALUES($1,$2,'published.bin',0) RETURNING id::STRING`, operationID, owner).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	handler := (&API{Accounts: accounts.Service{Pool: pool}, Catalog: catalog.Service{Pool: pool}, Eligible: func(*http.Request) error { return nil }}).Handler()
	return ctx, pool, handler, ownerCookie, otherCookie, owner, fileID, operationID
}
