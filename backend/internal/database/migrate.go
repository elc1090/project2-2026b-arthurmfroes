package database

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationNames = []string{"001_initial.sql", "002_node_faults.sql", "003_node_lifecycle.sql", "004_file_deletions.sql"}

// Migrate serializes callers through a row lock and commits initial CREATE TABLE
// statements together with their checksums. Existing migration files are immutable.
// Future ALTER migrations need a separate review of Cockroach's transactional DDL
// restrictions; this runner does not promise atomicity for arbitrary schema jobs.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout='30s'; SET LOCAL transaction_timeout='30s'"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration_lock (
   singleton BOOL PRIMARY KEY DEFAULT true CHECK (singleton)
  );
  CREATE TABLE IF NOT EXISTS schema_migrations (
   version INT8 PRIMARY KEY CHECK (version>0),
   name STRING NOT NULL UNIQUE,
   checksum BYTES NOT NULL CHECK (length(checksum)=32),
   applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  INSERT INTO schema_migration_lock(singleton) VALUES(true) ON CONFLICT DO NOTHING;`)
		return err
	}); err != nil {
		return fmt.Errorf("initialize migration ledger: %w", err)
	}
	return WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout='30s'; SET LOCAL transaction_timeout='30s'"); err != nil {
			return err
		}
		var locked bool
		if err := tx.QueryRow(ctx, "SELECT singleton FROM schema_migration_lock WHERE singleton=true FOR UPDATE").Scan(&locked); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT version,name,checksum FROM schema_migrations ORDER BY version")
		if err != nil {
			return err
		}
		applied := 0
		for rows.Next() {
			var version int64
			var name string
			var checksum []byte
			if err = rows.Scan(&version, &name, &checksum); err != nil {
				rows.Close()
				return err
			}
			if version != int64(applied+1) || version > int64(len(migrationNames)) || name != migrationNames[applied] {
				rows.Close()
				return fmt.Errorf("unknown or out-of-order migration version %d", version)
			}
			content, err := migrationFiles.ReadFile("migrations/" + name)
			if err != nil {
				rows.Close()
				return err
			}
			expected := sha256.Sum256(content)
			if string(checksum) != string(expected[:]) {
				rows.Close()
				return fmt.Errorf("migration %d checksum mismatch", version)
			}
			applied++
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for i := applied; i < len(migrationNames); i++ {
			name := migrationNames[i]
			content, err := migrationFiles.ReadFile("migrations/" + name)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, string(content)); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			checksum := sha256.Sum256(content)
			if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)", int64(i+1), name, checksum[:]); err != nil {
				return err
			}
		}
		return nil
	})
}
