package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

// AcquireLease increments the term whenever an empty/expired lease is acquired,
// including reacquisition by the previous holder. Use RenewLease while it is live.
func (s *Store) AcquireLease(ctx context.Context, holder string, ttl time.Duration) (Lease, error) {
	if holder == "" || ttl < time.Millisecond {
		return Lease{}, ErrInvalid
	}
	var lease Lease
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLease(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `UPDATE manager_lease SET holder_id=$1,term=term+1,expires_at=clock_timestamp()+($2::INT8 * INTERVAL '1 microsecond')
   WHERE singleton=true AND (holder_id IS NULL OR expires_at<=clock_timestamp())
   AND EXISTS(SELECT 1 FROM cluster_nodes WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM node_infrastructure i WHERE i.node_id=cluster_nodes.id AND i.blocked) AND state IN ('joining','syncing','ready','unavailable'))
   RETURNING holder_id::STRING,term,expires_at`, holder, ttl.Microseconds()).Scan(&lease.HolderID, &lease.Term, &lease.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAuthority
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind)
   SELECT $1,version,$2,'manager_elected' FROM cluster_configuration WHERE singleton=true`, holder, lease.Term)
		return err
	})
	if err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func (s *Store) RenewLease(ctx context.Context, authority Lease, ttl time.Duration) (Lease, error) {
	if ttl < time.Millisecond {
		return Lease{}, ErrInvalid
	}
	var lease Lease
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLease(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `UPDATE manager_lease SET expires_at=clock_timestamp()+($3::INT8 * INTERVAL '1 microsecond')
   WHERE singleton=true AND holder_id=$1 AND term=$2 AND expires_at>clock_timestamp()
   AND EXISTS(SELECT 1 FROM cluster_nodes WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM node_infrastructure i WHERE i.node_id=cluster_nodes.id AND i.blocked) AND state IN ('joining','syncing','ready','unavailable'))
   RETURNING holder_id::STRING,term,expires_at`, authority.HolderID, authority.Term, ttl.Microseconds()).Scan(&lease.HolderID, &lease.Term, &lease.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAuthority
		}
		return err
	})
	if err != nil {
		return Lease{}, err
	}
	return lease, nil
}

// Lock before testing clock_timestamp(): now() is fixed at transaction start and
// may already be stale when a contended row lock is finally acquired.
func lockLease(ctx context.Context, tx pgx.Tx) error {
	var singleton bool
	return tx.QueryRow(ctx, "SELECT singleton FROM manager_lease WHERE singleton=true FOR UPDATE").Scan(&singleton)
}

func checkAuthority(ctx context.Context, tx pgx.Tx, a Lease) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM manager_lease WHERE singleton=true AND holder_id=$1 AND term=$2 AND expires_at>clock_timestamp())`, a.HolderID, a.Term).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrNoAuthority
	}
	return nil
}

// Exclude atomically removes one node from both routing and required copies,
// through the shared membership set. It preserves identity, bytes and receipts.
// There is intentionally no generic setter that can admit an unsynchronized node.
func (s *Store) Exclude(ctx context.Context, a Lease, nodeID, reason string) (int64, error) {
	if reason == "" || nodeID == "" {
		return 0, ErrInvalid
	}
	var version int64
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Every lease-changing/membership-changing operation takes the lease first.
		if err := lockLease(ctx, tx); err != nil {
			return err
		}
		if err := checkAuthority(ctx, tx, a); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&version); err != nil {
			return err
		}
		var state string
		err := tx.QueryRow(ctx, "SELECT state FROM cluster_nodes WHERE id=$1 FOR UPDATE", nodeID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state == "removed" {
			return ErrConflict
		}
		removed, err := tx.Exec(ctx, "DELETE FROM cluster_membership WHERE node_id=$1", nodeID)
		if err != nil {
			return err
		}
		if state != "unavailable" || removed.RowsAffected() > 0 {
			if _, err = tx.Exec(ctx, "UPDATE cluster_nodes SET state='unavailable',reason=$2,transitioned_at=clock_timestamp() WHERE id=$1", nodeID, reason); err != nil {
				return err
			}
			if err = tx.QueryRow(ctx, "UPDATE cluster_configuration SET version=version+1,updated_at=clock_timestamp() WHERE singleton=true RETURNING version").Scan(&version); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind,details) VALUES($1,$2,$3,'node_excluded',jsonb_build_object('reason',$4::STRING))`, nodeID, version, a.Term, reason); err != nil {
				return err
			}
		}
		return checkAuthority(ctx, tx, a)
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}
