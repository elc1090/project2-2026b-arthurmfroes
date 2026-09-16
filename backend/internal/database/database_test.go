package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOpenInvalidURLDoesNotExposeCredentials(t *testing.T) {
	_, err := Open(context.Background(), "postgresql://user:secret-value@host:bad/db")
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("unsafe parse result: %v", err)
	}
}

func TestOpenDoesNotRequireAvailableDatabase(t *testing.T) {
	pool, err := Open(context.Background(), "postgresql://user@127.0.0.1:1/absent?sslmode=disable&pool_max_conns=2")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Config().MaxConns != 2 {
		t.Fatal("pool URL option not honored")
	}
}

// Run against a dedicated database, e.g. ACERVO_TEST_DATABASE_URL=
// postgresql://root@127.0.0.1:27657/acervo_database_test?sslmode=disable.
// Each invocation creates and removes only its own uniquely named schema.
func TestCockroachIntegration(t *testing.T) {
	raw := os.Getenv("ACERVO_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ACERVO_TEST_DATABASE_URL is required for real CockroachDB tests")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/acervo_database_test" {
		t.Fatal("integration tests require the dedicated acervo_database_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adminURL := *u
	adminURL.Path = "/defaultdb"
	admin, err := Open(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err = admin.Exec(ctx, "CREATE DATABASE IF NOT EXISTS acervo_database_test"); err != nil {
		t.Fatal(err)
	}
	base, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := base.Exec(clean, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("pool_max_conns", "6")
	u.RawQuery = q.Encode()
	pool, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var version string
	if err = pool.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Log(version)
	t.Run("concurrent cold migrations and replay", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan error, 3)
		start := make(chan struct{})
		for range 3 {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; results <- Migrate(ctx, pool) }()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 4 {
			t.Fatalf("migration count=%d err=%v", count, err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema=$1", schema).Scan(&count); err != nil || count != 21 {
			t.Fatalf("table count=%d err=%v", count, err)
		}
	})
	if t.Failed() {
		return
	}
	t.Run("server expires abandoned transaction locks", func(t *testing.T) {
		held, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Rollback(context.Background())
		var singleton bool
		if err := held.QueryRow(ctx, "SELECT singleton FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&singleton); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		// Start the contender later so its own statement deadline does not race
		// the abandoned session's independently measured five-second deadline.
		time.Sleep(time.Second)
		wait, cancel := context.WithTimeout(ctx, 9*time.Second)
		defer cancel()
		// Keep the first client connected but silent. Only a server-side timeout
		// can release its lock; this test does not send cancellation or rollback.
		if err := WithTx(wait, pool, func(tx pgx.Tx) error {
			return tx.QueryRow(wait, "SELECT singleton FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&singleton)
		}); err != nil {
			t.Fatal("abandoned lock prevented successor", err)
		}
		elapsed := time.Since(started)
		if elapsed < 4*time.Second || elapsed > 9*time.Second {
			t.Fatal("unexpected lock expiry", elapsed)
		}
		if err := held.Commit(ctx); err == nil {
			t.Fatal("expired transaction committed")
		}
		t.Logf("server released abandoned lock in %s; old transaction rejected", elapsed)
	})

	if _, err = pool.Exec(ctx, "CREATE TABLE retry_probe(id INT8 PRIMARY KEY,value INT8 NOT NULL); INSERT INTO retry_probe VALUES(1,0)"); err != nil {
		t.Fatal(err)
	}
	t.Run("real serialization retry", func(t *testing.T) {
		read := make(chan struct{})
		updated := make(chan struct{})
		result := make(chan error, 1)
		var attempts atomic.Int32
		go func() {
			result <- WithTx(ctx, pool, func(tx pgx.Tx) error {
				n := attempts.Add(1)
				var value int64
				if err := tx.QueryRow(ctx, "SELECT value FROM retry_probe WHERE id=1").Scan(&value); err != nil {
					return err
				}
				if n == 1 {
					close(read)
					select {
					case <-updated:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				_, err := tx.Exec(ctx, "UPDATE retry_probe SET value=$1 WHERE id=1", value+1)
				return err
			})
		}()
		select {
		case <-read:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		_, updateErr := pool.Exec(ctx, "UPDATE retry_probe SET value=value+1 WHERE id=1")
		close(updated)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		var value int64
		if err := pool.QueryRow(ctx, "SELECT value FROM retry_probe WHERE id=1").Scan(&value); err != nil {
			t.Fatal(err)
		}
		if attempts.Load() < 2 || value != 2 {
			t.Fatalf("attempts=%d value=%d", attempts.Load(), value)
		}
		t.Logf("real conflict: %d callback attempts, final value %d", attempts.Load(), value)
	})
	t.Run("rollback generic error without retry", func(t *testing.T) {
		sentinel := errors.New("application error")
		calls := 0
		err := WithTx(ctx, pool, func(tx pgx.Tx) error {
			calls++
			if _, err := tx.Exec(ctx, "INSERT INTO retry_probe VALUES(2,2)"); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		assertProbeAbsent(t, ctx, pool, 2)
	})
	t.Run("bounded retries roll back every attempt", func(t *testing.T) {
		calls := 0
		err := WithTx(ctx, pool, func(tx pgx.Tx) error {
			calls++
			if _, err := tx.Exec(ctx, "INSERT INTO retry_probe VALUES(3,3)"); err != nil {
				return err
			}
			return &pgconn.PgError{Code: "40001", Message: "test retry"}
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" || calls != maxAttempts {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		assertProbeAbsent(t, ctx, pool, 3)
	})
	t.Run("ambiguous result never retries", func(t *testing.T) {
		calls := 0
		err := WithTx(ctx, pool, func(tx pgx.Tx) error { calls++; return &pgconn.PgError{Code: "40003", Message: "ambiguous"} })
		if err == nil || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("cancel retry wait", func(t *testing.T) {
		cancelled, stop := context.WithCancel(ctx)
		calls := 0
		start := time.Now()
		err := WithTx(cancelled, pool, func(tx pgx.Tx) error { calls++; stop(); return &pgconn.PgError{Code: "40001"} })
		if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) > time.Second {
			t.Fatalf("calls=%d err=%v elapsed=%v", calls, err, time.Since(start))
		}
	})
	t.Run("schema constraints", func(t *testing.T) { testConstraints(t, ctx, pool) })
	t.Run("replay preserves application data", func(t *testing.T) {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil || count != 2 {
			t.Fatalf("users=%d err=%v", count, err)
		}
	})
	t.Run("checksum mismatch rejected", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET checksum=$1 WHERE version=1", make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
		if err := Migrate(ctx, pool); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("unexpected migration result: %v", err)
		}
	})
}

func assertProbeAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM retry_probe WHERE id=$1", id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unrolled rows=%d err=%v", count, err)
	}
}

func testConstraints(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var owner, other, folder, operation, node string
	for _, entry := range []struct {
		login string
		dest  *string
	}{{"alice", &owner}, {"bob", &other}} {
		if err := pool.QueryRow(ctx, "INSERT INTO users(login,password_hash) VALUES($1,'hash') RETURNING id::STRING", entry.login).Scan(entry.dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, "INSERT INTO folders(owner_id,name) VALUES($1,'root') RETURNING id::STRING", owner).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	hash := make([]byte, 32)
	if err := pool.QueryRow(ctx, `INSERT INTO upload_operations(owner_id,folder_id,name,idempotency_key,size_bytes,sha256,manifest,part_count) VALUES($1,$2,'large',gen_random_uuid(),4294967296,$3,'[]',1) RETURNING id::STRING`, owner, folder, hash).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO upload_parts VALUES($1,0,2147483648,2147483648,$2)", operation, hash); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO cluster_nodes(node_id,backend_endpoint,database_endpoint,storage_endpoint) VALUES('node-1','http://backend','postgresql://db','http://storage') RETURNING id::STRING").Scan(&node); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, query, code string
		args              []any
	}{
		{"duplicate root folder", "INSERT INTO folders(owner_id,name) VALUES($1,'root')", "23505", []any{owner}},
		{"foreign folder parent", "INSERT INTO folders(owner_id,parent_id,name) VALUES($1,$2,'child')", "23503", []any{other, folder}},
		{"negative size", "UPDATE upload_operations SET size_bytes=-1 WHERE id=$1", "23514", []any{operation}},
		{"invalid checksum", "UPDATE upload_operations SET sha256=$2 WHERE id=$1", "23514", []any{operation, []byte{1}}},
		{"foreign file owner", "INSERT INTO files(operation_id,owner_id,name,configuration_version) VALUES($1,$2,'stolen',0)", "23503", []any{operation, other}},
		{"cancelled complete contradiction", "UPDATE upload_operations SET status='cancelled',phase='complete' WHERE id=$1", "23514", []any{operation}},
		{"receipt differing size", `INSERT INTO upload_part_copies(operation_id,part_index,node_id,storage_generation,object_key,s3_version_id,size_bytes,sha256) VALUES($1,0,$2,gen_random_uuid(),'part','v1',1,$3)`, "23503", []any{operation, node, hash}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.query, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
}
