package cluster

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Guard fences a user transaction against configuration changes. Invoke it before
// and after the mutation in the SAME transaction. It locks configuration, never
// the lease row, preserving the manager's lease -> configuration lock order.
// The HTTP layer must additionally verify local storage health before admission.
func Guard(ctx context.Context, tx pgx.Tx, nodeID string) error {
	var version int64
	if err := tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&version); err != nil {
		return err
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cluster_nodes n JOIN cluster_membership m ON m.node_id=n.id
  WHERE n.id=$1 AND n.state='ready') AND EXISTS(SELECT 1 FROM manager_lease WHERE singleton=true AND holder_id IS NOT NULL AND expires_at>clock_timestamp())`, nodeID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrNoAuthority
	}
	return nil
}
