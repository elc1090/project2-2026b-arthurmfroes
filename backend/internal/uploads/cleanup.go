package uploads

import (
	"context"
	"errors"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/jackc/pgx/v5"
)

// RunCleanup revisits terminal operations: an in-flight cancelled PUT may leave
// a later orphan. Every node cleans its own endpoint, without deleting finals.
func (s *Service) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = s.CleanupDeletedFiles(ctx)
		_, _ = s.Cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CleanupDeletedFiles removes published versions only while their tombstone,
// receipt and registered storage generation still describe the same object.
// Ambiguous storage errors leave the receipt durable so a later pass retries.
func (s *Service) CleanupDeletedFiles(ctx context.Context) (int, error) {
	type candidate struct {
		operationID, nodeID, generation, key, version string
		node                                          cluster.Node
	}
	const zero = "00000000-0000-0000-0000-000000000000"
	afterOperation, afterNode, afterGeneration := zero, zero, zero
	removed := 0
	var result error
	for {
		var candidates []candidate
		err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT c.operation_id::STRING,c.node_id::STRING,c.storage_generation::STRING,
 c.object_key,c.s3_version_id,n.id::STRING,n.node_id,n.backend_endpoint,n.database_endpoint,
 n.storage_endpoint,n.storage_generation::STRING,n.state
 FROM file_deletions d JOIN object_copies c ON c.operation_id=d.operation_id
 JOIN cluster_nodes n ON n.id=c.node_id
 WHERE c.storage_generation=n.storage_generation
 AND (c.operation_id,c.node_id,c.storage_generation)>($1::UUID,$2::UUID,$3::UUID)
 ORDER BY c.operation_id,c.node_id,c.storage_generation LIMIT 50`, afterOperation, afterNode, afterGeneration)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var item candidate
				if err := rows.Scan(&item.operationID, &item.nodeID, &item.generation, &item.key, &item.version,
					&item.node.ID, &item.node.NodeID, &item.node.BackendEndpoint, &item.node.DatabaseEndpoint,
					&item.node.StorageEndpoint, &item.node.StorageGeneration, &item.node.State); err != nil {
					return err
				}
				candidates = append(candidates, item)
			}
			return rows.Err()
		})
		if err != nil {
			return removed, errors.Join(result, err)
		}
		if len(candidates) == 0 {
			return removed, result
		}
		for _, item := range candidates {
			store, err := s.cfg.StorageFor(item.node)
			if err == nil {
				err = store.VisitFinalVersions(ctx, item.key, func(key, version string) error {
					return store.RemoveFinalVersion(ctx, key, version)
				})
			}
			if err == nil {
				err = s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					tag, err := tx.Exec(ctx, `DELETE FROM object_copies c
 WHERE c.operation_id=$1::UUID AND c.node_id=$2::UUID AND c.storage_generation=$3::UUID
 AND c.object_key=$4 AND c.s3_version_id=$5
 AND EXISTS(SELECT 1 FROM file_deletions d WHERE d.operation_id=c.operation_id)
 AND EXISTS(SELECT 1 FROM cluster_nodes n WHERE n.id=c.node_id AND n.storage_generation=c.storage_generation)`,
						item.operationID, item.nodeID, item.generation, item.key, item.version)
					if err == nil {
						removed += int(tag.RowsAffected())
					}
					return err
				})
			}
			result = errors.Join(result, err)
			if ctx.Err() != nil {
				return removed, errors.Join(result, ctx.Err())
			}
		}
		last := candidates[len(candidates)-1]
		afterOperation, afterNode, afterGeneration = last.operationID, last.nodeID, last.generation
	}
}

// Cleanup removes local temporary physical versions of terminal operations.
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	removed := 0
	after := "00000000-0000-0000-0000-000000000000"
	var result error
	for {
		type candidate struct {
			id              string
			hasLocalReceipt bool
		}
		var candidates []candidate
		err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			candidates = nil
			rows, err := tx.Query(ctx, `SELECT o.id::STRING, EXISTS(SELECT 1 FROM upload_part_copies c
 WHERE c.operation_id=o.id AND c.node_id=$2::UUID AND c.storage_generation=$3::UUID)
 FROM upload_operations o WHERE o.status IN ('cancelled','available') AND o.id>$1::UUID ORDER BY o.id LIMIT 50`, after, s.cfg.LocalNode.ID, s.cfg.LocalNode.StorageGeneration)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var item candidate
				if err := rows.Scan(&item.id, &item.hasLocalReceipt); err != nil {
					return err
				}
				candidates = append(candidates, item)
			}
			return rows.Err()
		})
		if err != nil {
			return removed, errors.Join(result, err)
		}
		if len(candidates) == 0 {
			return removed, result
		}
		for _, item := range candidates {
			id := item.id
			err := s.cfg.LocalStorage.VisitPartVersions(ctx, id, func(key, version string) error {
				safe := false
				err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					// Terminal status never returns to pending. A late PUT may finish I/O,
					// but cannot record a receipt or create a new active part reference.
					return tx.QueryRow(ctx, `SELECT o.status IN ('cancelled','available')
      AND n.storage_generation=$2::UUID AND n.state='ready'
      AND NOT EXISTS(SELECT 1 FROM object_copies c WHERE c.object_key=$3)
      AND NOT EXISTS(SELECT 1 FROM upload_part_copies c JOIN upload_operations live ON live.id=c.operation_id WHERE c.object_key=$3 AND live.status='pending')
      FROM upload_operations o CROSS JOIN cluster_nodes n WHERE o.id=$1::UUID AND n.id=$4::UUID`, id, s.cfg.LocalNode.StorageGeneration, key, s.cfg.LocalNode.ID).Scan(&safe)
				})
				if err != nil {
					return err
				}
				if !safe {
					return nil
				}
				if err := s.cfg.LocalStorage.RemovePartVersion(ctx, id, key, version); err != nil {
					return err
				}
				removed++
				return s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `DELETE FROM upload_part_copies WHERE operation_id=$1::UUID AND node_id=$2::UUID AND storage_generation=$3::UUID AND object_key=$4 AND s3_version_id=$5`, id, s.cfg.LocalNode.ID, s.cfg.LocalNode.StorageGeneration, key, version)
					return err
				})
			})
			if err == nil && item.hasLocalReceipt {
				// Terminal operations cannot gain a new SQL receipt. When the
				// page found none, listing still catches late orphan versions,
				// but there is no receipt-retirement transaction to perform.
				// Replicated version deletion may remove bytes on this site before
				// its scan. Successful complete listing also retires those receipts.
				err = s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `DELETE FROM upload_part_copies c WHERE c.operation_id=$1::UUID
					 AND c.node_id=$2::UUID AND c.storage_generation=$3::UUID
					 AND EXISTS(SELECT 1 FROM upload_operations o WHERE o.id=c.operation_id AND o.status IN ('available','cancelled'))
					 AND NOT EXISTS(SELECT 1 FROM object_copies final WHERE final.object_key=c.object_key)
					 AND NOT EXISTS(SELECT 1 FROM upload_part_copies ref JOIN upload_operations live ON live.id=ref.operation_id WHERE ref.object_key=c.object_key AND live.status='pending')`, id, s.cfg.LocalNode.ID, s.cfg.LocalNode.StorageGeneration)
					return err
				})
			}
			result = errors.Join(result, err)
			if ctx.Err() != nil {
				return removed, errors.Join(result, ctx.Err())
			}
		}
		after = candidates[len(candidates)-1].id
	}
}
