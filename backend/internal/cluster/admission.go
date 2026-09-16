package cluster

import (
	"context"
	"errors"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

var ErrSyncRequired = errors.New("node synchronization or publication generation changed")

// RecoveryFile is internal storage work, never an administrative API response.
// A receipt flag describes SQL evidence, not an independent check of stored bytes.
type RecoveryFile struct {
	OperationID  string
	SizeBytes    int64
	SHA256       []byte
	FreshReceipt bool
}
type RecoveryPlan struct {
	Node                  Node
	PublicationGeneration int64
	Files                 []RecoveryFile
}

// StartSync removes any inconsistent membership before recovery. Repeated calls
// preserve the start timestamp, so completed verification work remains usable.
func (s *Store) StartSync(ctx context.Context, a Lease, nodeID string) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLease(ctx, tx); err != nil {
			return err
		}
		if err := checkAuthority(ctx, tx, a); err != nil {
			return err
		}
		var version int64
		if err := tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&version); err != nil {
			return err
		}
		var state string
		var blocked bool
		err := tx.QueryRow(ctx, "SELECT state,EXISTS(SELECT 1 FROM node_infrastructure i WHERE i.node_id=cluster_nodes.id AND i.blocked) FROM cluster_nodes WHERE id=$1 FOR UPDATE", nodeID).Scan(&state, &blocked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if blocked {
			return ErrConflict
		}
		if state != "joining" && state != "unavailable" && state != "syncing" {
			return ErrConflict
		}
		deleted, err := tx.Exec(ctx, "DELETE FROM cluster_membership WHERE node_id=$1", nodeID)
		if err != nil {
			return err
		}
		if deleted.RowsAffected() > 0 {
			if err := tx.QueryRow(ctx, "UPDATE cluster_configuration SET version=version+1,updated_at=clock_timestamp() WHERE singleton=true RETURNING version").Scan(&version); err != nil {
				return err
			}
		}
		if state != "syncing" {
			if _, err := tx.Exec(ctx, "UPDATE cluster_nodes SET state='syncing',reason=NULL,transitioned_at=clock_timestamp() WHERE id=$1", nodeID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind) VALUES($1,$2,$3,'node_sync_started')", nodeID, version, a.Term); err != nil {
				return err
			}
		}
		return checkAuthority(ctx, tx, a)
	})
}

// AdmissionPlan returns a coherent list of all published versions to verify or
// repair. Actual reads, checksums and local receipts happen outside transactions.
func (s *Store) AdmissionPlan(ctx context.Context, nodeID string) (RecoveryPlan, error) {
	var plan RecoveryPlan
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		plan = RecoveryPlan{Files: []RecoveryFile{}}
		if err := tx.QueryRow(ctx, "SELECT publication_generation FROM cluster_configuration WHERE singleton=true").Scan(&plan.PublicationGeneration); err != nil {
			return err
		}
		err := scanNode(tx.QueryRow(ctx, `SELECT id::STRING,node_id,backend_endpoint,database_endpoint,storage_endpoint,storage_generation::STRING,state FROM cluster_nodes WHERE id=$1`, nodeID), &plan.Node)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if plan.Node.State != "syncing" {
			return ErrConflict
		}
		rows, err := tx.Query(ctx, `SELECT u.id::STRING,u.size_bytes,u.sha256,
   EXISTS(SELECT 1 FROM object_copies c JOIN cluster_nodes n ON n.id=c.node_id
    WHERE c.operation_id=u.id AND n.id=$1 AND c.storage_generation=n.storage_generation
    AND c.size_bytes=u.size_bytes AND c.sha256=u.sha256 AND c.verified_at>=n.transitioned_at)
   FROM files f JOIN upload_operations u ON u.id=f.operation_id ORDER BY u.id`, nodeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f RecoveryFile
			if err := rows.Scan(&f.OperationID, &f.SizeBytes, &f.SHA256, &f.FreshReceipt); err != nil {
				return err
			}
			plan.Files = append(plan.Files, f)
		}
		return rows.Err()
	})
	if err != nil {
		return RecoveryPlan{}, err
	}
	return plan, nil
}

// Admit performs only the SQL barrier. Its caller must verify current component
// health and produce fresh local receipts after StartSync. Publication writers
// must lock the same configuration row and increment publication_generation.
func (s *Store) Admit(ctx context.Context, a Lease, nodeID string, expectedPublicationGeneration int64) (int64, error) {
	if expectedPublicationGeneration < 0 {
		return 0, ErrInvalid
	}
	var version int64
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockLease(ctx, tx); err != nil {
			return err
		}
		if err := checkAuthority(ctx, tx, a); err != nil {
			return err
		}
		var generation int64
		if err := tx.QueryRow(ctx, "SELECT version,publication_generation FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&version, &generation); err != nil {
			return err
		}
		if generation != expectedPublicationGeneration {
			return ErrSyncRequired
		}
		var state string
		var blocked bool
		err := tx.QueryRow(ctx, "SELECT state,EXISTS(SELECT 1 FROM node_infrastructure i WHERE i.node_id=cluster_nodes.id AND i.blocked) FROM cluster_nodes WHERE id=$1 FOR UPDATE", nodeID).Scan(&state, &blocked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if blocked || state != "syncing" {
			return ErrConflict
		}
		var missing bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM files f JOIN upload_operations u ON u.id=f.operation_id
   WHERE NOT EXISTS(SELECT 1 FROM object_copies c JOIN cluster_nodes n ON n.id=c.node_id
    WHERE c.operation_id=u.id AND n.id=$1 AND c.storage_generation=n.storage_generation
    AND c.size_bytes=u.size_bytes AND c.sha256=u.sha256 AND c.verified_at>=n.transitioned_at))
 OR EXISTS(SELECT 1 FROM file_deletions d JOIN object_copies c ON c.operation_id=d.operation_id
    JOIN cluster_nodes n ON n.id=c.node_id
    WHERE n.id=$1 AND c.storage_generation=n.storage_generation)`, nodeID).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrSyncRequired
		}
		if _, err := tx.Exec(ctx, "UPDATE cluster_nodes SET state='ready',synced_publication_generation=$2,transitioned_at=clock_timestamp(),reason=NULL WHERE id=$1", nodeID, generation); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO cluster_membership(node_id) VALUES($1) ON CONFLICT DO NOTHING", nodeID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "UPDATE cluster_configuration SET version=version+1,updated_at=clock_timestamp() WHERE singleton=true RETURNING version").Scan(&version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind) VALUES($1,$2,$3,'node_admitted')", nodeID, version, a.Term); err != nil {
			return err
		}
		return checkAuthority(ctx, tx, a)
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// Nodes is an internal registry view, including nodes outside membership.
func (s *Store) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::STRING,node_id,backend_endpoint,database_endpoint,storage_endpoint,storage_generation::STRING,state FROM cluster_nodes ORDER BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []Node{}
	for rows.Next() {
		var n Node
		if err := scanNode(rows, &n); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}
