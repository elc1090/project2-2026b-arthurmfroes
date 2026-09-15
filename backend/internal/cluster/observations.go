package cluster

import (
	"context"
	"encoding/json"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

// Observation uses nil for an unobservable component, e.g. SQL behind a dead
// backend. It carries no arbitrary error, request payload, names or credentials.
type Observation struct {
	Backend *bool `json:"backend"`
	SQL     *bool `json:"sql"`
	Storage *bool `json:"storage"`
	Control *bool `json:"control"`
}

func (s *Store) RecordObservation(ctx context.Context, a Lease, nodeID string, health Observation) error {
	payload, err := json.Marshal(health)
	if err != nil {
		return err
	}
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLease(ctx, tx); err != nil {
			return err
		}
		if err := checkAuthority(ctx, tx, a); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, "UPDATE cluster_nodes SET health=$2::JSONB,observed_at=clock_timestamp() WHERE id=$1", nodeID, string(payload))
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return ErrNotFound
		}
		return checkAuthority(ctx, tx, a)
	})
}
