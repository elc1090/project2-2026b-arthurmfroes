// Package database owns SQL connections, schema migrations and serializable retries.
package database

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates a lazy pool. An unavailable local database must not terminate the
// backend process; readiness and Migrate establish availability separately.
// The caller owns pool.Close. pgx pool settings may be supplied in the URL.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid database connection configuration")
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "acervo"
	// Client cancellation cannot deliver ROLLBACK across a network partition.
	// Bound abandoned SQL work on the server too, especially idle transactions
	// holding the configuration/manager locks. File I/O stays outside transactions.
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5s"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	cfg.ConnConfig.RuntimeParams["transaction_timeout"] = "10s"
	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

const maxAttempts = 5

// WithTx runs fn in a serializable transaction. fn may run repeatedly and must
// not perform external side effects or retain a transaction after returning.
// Only SQLSTATE 40001 is retried, including a definite serialization failure at
// commit. Network errors and ambiguous commits are returned without replay.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := transaction(ctx, pool, fn)
		if err == nil {
			return nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" || attempt == maxAttempts-1 {
			return err
		}
		delay := time.Duration(10*(1<<attempt)) * time.Millisecond
		timer := time.NewTimer(delay + time.Duration(rand.Int64N(int64(delay))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}

func transaction(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() {
		// A cancelled request must still release its connection before any retry.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
