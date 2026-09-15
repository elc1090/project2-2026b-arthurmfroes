package uploads

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// RunCleanup revisits terminal operations: an in-flight cancelled PUT may leave
// a later orphan. Every node cleans its own endpoint, without deleting finals.
func (s *Service) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = s.Cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
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
