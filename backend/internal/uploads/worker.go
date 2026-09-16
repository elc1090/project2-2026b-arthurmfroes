package uploads

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
)

const workerLease = 30 * time.Second

var errLeaseLost = errors.New("upload worker lease lost")

type claim struct {
	ID, Owner  string
	Generation int64
	Operation  Operation
}

// Run processes durable operations independently of browsers. Each node can run
// one loop; SQL leases keep distinct attempts from publishing each other's work.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, _ = s.processNext(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) claimNext(ctx context.Context) (claim, bool, error) {
	var c claim
	found := false
	err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		found = false
		err := tx.QueryRow(ctx, `SELECT id::STRING,owner_id::STRING FROM upload_operations
   WHERE status='pending' AND (lease_holder IS NULL OR lease_expires_at<=clock_timestamp())
   AND (error_code IS NULL OR error_code NOT IN ('content_mismatch','name_conflict'))
   ORDER BY updated_at,id LIMIT 1 FOR UPDATE`).Scan(&c.ID, &c.Owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `UPDATE upload_operations SET lease_holder=$2,lease_generation=lease_generation+1,
   lease_expires_at=clock_timestamp()+INTERVAL '30 seconds',updated_at=clock_timestamp()
   WHERE id=$1 RETURNING lease_generation`, c.ID, s.cfg.LocalNode.ID).Scan(&c.Generation); err != nil {
			return err
		}
		c.Operation, err = loadOperation(ctx, tx, c.Owner, c.ID, false)
		found = err == nil
		return err
	})
	return c, found, err
}
func (s *Service) fencedTx(ctx context.Context, c claim, fn func(context.Context, pgx.Tx) error) error {
	return s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var owned bool
		if err := tx.QueryRow(ctx, `SELECT status='pending' AND lease_holder=$2 AND lease_generation=$3 AND lease_expires_at>clock_timestamp()
   FROM upload_operations WHERE id=$1 FOR UPDATE`, c.ID, s.cfg.LocalNode.ID, c.Generation).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return errLeaseLost
		}
		if err := fn(ctx, tx); err != nil {
			return err
		}
		// The same row remains locked. Expiry is checked again after the mutation.
		if err := tx.QueryRow(ctx, "SELECT lease_expires_at>clock_timestamp() FROM upload_operations WHERE id=$1", c.ID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return errLeaseLost
		}
		return nil
	})
}
func (s *Service) renew(ctx context.Context, c claim) error {
	return s.fencedTx(ctx, c, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE upload_operations SET lease_expires_at=clock_timestamp()+INTERVAL '30 seconds' WHERE id=$1", c.ID)
		return err
	})
}
func (s *Service) processNext(ctx context.Context) (bool, error) {
	c, found, err := s.claimNext(ctx)
	if err != nil || !found {
		return found, err
	}
	work, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(workerLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				if s.renew(work, c) != nil {
					cancel()
					return
				}
			}
		}
	}()
	err = s.process(work, c)
	cancel()
	<-done
	// Release only our still-current generation. A cancelled/published operation or
	// successor owns its own state; a lost response must not revert either outcome.
	if ctx.Err() == nil {
		_ = s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			code, phase := errorState(err)
			_, releaseErr := tx.Exec(ctx, `UPDATE upload_operations SET lease_holder=NULL,lease_expires_at=NULL,
   error_code=$4,phase=$5,updated_at=clock_timestamp() WHERE id=$1 AND status='pending' AND lease_holder=$2 AND lease_generation=$3`, c.ID, s.cfg.LocalNode.ID, c.Generation, code, phase)
			return releaseErr
		})
	}
	return true, err
}
func errorState(err error) (*string, string) {
	code := "storage_unavailable"
	phase := "receiving"
	var response minio.ErrorResponse
	if errors.As(err, &response) && (response.Code == "XMinioStorageFull" || response.Code == "InsufficientStorage") {
		code = "capacity_exceeded"
	}
	switch {
	case err == nil:
		return nil, "receiving"
	case errors.Is(err, storage.ErrIntegrity):
		code = "content_mismatch"
	case errors.Is(err, ErrConflict), errors.Is(err, catalog.ErrConflict):
		code = "name_conflict"
	case errors.Is(err, ErrUnavailable):
		code = "awaiting_parts"
		phase = "awaiting_parts"
	}
	return &code, phase
}
func (s *Service) process(ctx context.Context, c claim) error {
	snapshot, err := s.cfg.Cluster.Snapshot(ctx)
	if err != nil {
		return err
	}
	if len(snapshot.Members) == 0 {
		return cluster.ErrNoAuthority
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.cancelOnConfigurationChange(work, cancel, snapshot.Version)
	ctx = work
	sum, err := digest(c.Operation.SHA256)
	if err != nil {
		return err
	}
	for _, node := range snapshot.Members {
		if err := s.cfg.Eligible(ctx); err != nil {
			return err
		}
		target, err := s.cfg.StorageFor(node)
		if err != nil {
			return err
		}
		source := s.partsReader(ctx, c)
		receipt, writeErr := target.Write(ctx, "objects/"+c.ID, c.Operation.Size, sum, source)
		_ = source.Close()
		if writeErr != nil {
			return writeErr
		}
		err = s.fencedTx(ctx, c, func(ctx context.Context, tx pgx.Tx) error {
			if err := saveFinalReceipt(ctx, tx, c.ID, node, receipt, false); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE upload_operations SET phase='confirming',error_code=NULL,configuration_version=$2,updated_at=clock_timestamp() WHERE id=$1", c.ID, snapshot.Version)
			return err
		})
		if err != nil {
			return err
		}
	}
	return s.publish(ctx, c)
}

// A worker may be streaming a large object when the manager removes one of its
// destinations. Stop that attempt as soon as the authoritative membership
// version changes so the durable operation can be claimed again with a fresh
// snapshot instead of waiting on the failed storage connection.
func (s *Service) cancelOnConfigurationChange(ctx context.Context, cancel context.CancelFunc, version int64) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var current int64
			if err := s.cfg.Pool.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true").Scan(&current); err == nil && current != version {
				cancel()
				return
			}
		}
	}
}

// partsReader validates one bounded browser part before exposing it to assembly.
// This permits fallback to another copy on corruption without emitting a bad
// prefix. Buffers are bounded by the 32 MiB part size, never the complete file.
func (s *Service) partsReader(ctx context.Context, c claim) io.ReadCloser {
	ctx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	go func() {
		var err error
		defer func() { _ = writer.CloseWithError(err) }()
		for _, part := range c.Operation.Parts {
			var body []byte
			body, err = s.readPart(ctx, c, part)
			if err != nil {
				return
			}
			if _, err = writer.Write(body); err != nil {
				return
			}
		}
	}()
	return &partsPipe{PipeReader: reader, cancel: cancel}
}

type partsPipe struct {
	*io.PipeReader
	cancel context.CancelFunc
}

func (p *partsPipe) Close() error { p.cancel(); return p.PipeReader.Close() }
func (s *Service) readPart(ctx context.Context, c claim, part Part) ([]byte, error) {
	sources, err := s.partSources(ctx, c.ID, part.Index)
	if err != nil {
		return nil, err
	}
	expected, err := digest(part.SHA256)
	if err != nil {
		return nil, err
	}
	corrupt := []copyLocation{}
	for _, source := range sources {
		store, err := s.cfg.StorageFor(source.Node)
		if err != nil {
			continue
		}
		reader, info, err := store.Open(ctx, source.Key, source.Version)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(reader, part.Size+1))
		_ = reader.Close()
		if readErr != nil {
			continue
		}
		if info.Size == part.Size && int64(len(body)) == part.Size && sha256.Sum256(body) == expected {
			return body, nil
		}
		corrupt = append(corrupt, source)
	}
	if len(corrupt) > 0 {
		// Every available copy failed verification. Invalidate only observed corrupt locations
		// under the worker fence, so the client can supply that part again.
		if err := s.fencedTx(ctx, c, func(ctx context.Context, tx pgx.Tx) error {
			for _, bad := range corrupt {
				if _, err := tx.Exec(ctx, "DELETE FROM upload_part_copies WHERE operation_id=$1 AND part_index=$2 AND node_id=$3 AND storage_generation=$4 AND s3_version_id=$5", c.ID, part.Index, bad.Node.ID, bad.Node.StorageGeneration, bad.Version); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return nil, ErrUnavailable
}
func saveFinalReceipt(ctx context.Context, tx pgx.Tx, id string, node cluster.Node, receipt storage.Receipt, syncing bool) error {
	var generation, state string
	if err := tx.QueryRow(ctx, "SELECT storage_generation::STRING,state FROM cluster_nodes WHERE id=$1", node.ID).Scan(&generation, &state); err != nil {
		return err
	}
	if generation != node.StorageGeneration || (syncing && state != "syncing") || (!syncing && state != "ready") {
		return ErrUnavailable
	}
	_, err := tx.Exec(ctx, `INSERT INTO object_copies(operation_id,node_id,storage_generation,object_key,s3_version_id,size_bytes,sha256,verified_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()) ON CONFLICT(operation_id,node_id,storage_generation)
 DO UPDATE SET object_key=excluded.object_key,s3_version_id=excluded.s3_version_id,verified_at=excluded.verified_at`, id, node.ID, generation, receipt.Key, receipt.VersionID, receipt.Size, receipt.SHA256[:])
	return err
}
func (s *Service) publish(ctx context.Context, c claim) error {
	return s.fencedTx(ctx, c, func(ctx context.Context, tx pgx.Tx) error {
		var version int64
		var count int64
		var missing bool
		if err := tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true").Scan(&version); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(n.state<>'ready' OR NOT EXISTS(SELECT 1 FROM object_copies c
   JOIN upload_operations u ON u.id=c.operation_id WHERE c.operation_id=$1 AND c.node_id=n.id
   AND c.storage_generation=n.storage_generation AND c.size_bytes=u.size_bytes AND c.sha256=u.sha256)),false)
   FROM cluster_membership m JOIN cluster_nodes n ON n.id=m.node_id`, c.ID).Scan(&count, &missing); err != nil {
			return err
		}
		if count == 0 || missing {
			return ErrUnavailable
		}
		if err := catalog.LockDirectory(ctx, tx, c.Owner, c.Operation.FolderID); err != nil {
			return err
		}
		if err := catalog.CheckName(ctx, tx, c.Owner, c.Operation.FolderID, c.Operation.Name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO files(operation_id,owner_id,folder_id,name,configuration_version) VALUES($1,$2,$3,$4,$5)", c.ID, c.Owner, c.Operation.FolderID, c.Operation.Name, version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE upload_operations SET status='available',phase='complete',configuration_version=$2,error_code=NULL,updated_at=clock_timestamp() WHERE id=$1", c.ID, version); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE cluster_configuration SET publication_generation=publication_generation+1,updated_at=clock_timestamp() WHERE singleton=true")
		return err
	})
}

// Download authorizes the published file and selects a receipt for this node's
// current storage generation. The read remains bound to that physical version.
func (s *Service) Download(ctx context.Context, owner, fileID string) (io.ReadCloser, string, int64, error) {
	var key, version, name string
	var size int64
	err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT c.object_key,c.s3_version_id,f.name,u.size_bytes FROM files f
   JOIN upload_operations u ON u.id=f.operation_id AND u.status='available'
   JOIN object_copies c ON c.operation_id=u.id
   JOIN cluster_nodes n ON n.id=c.node_id AND n.storage_generation=c.storage_generation
   WHERE f.id=$1 AND f.owner_id=$2 AND c.node_id=$3`, fileID, owner, s.cfg.LocalNode.ID).Scan(&key, &version, &name, &size)
	})
	if err != nil {
		return nil, "", 0, err
	}
	streamContext, cancel := context.WithCancel(ctx)
	reader, info, err := s.cfg.LocalStorage.Open(streamContext, key, version)
	if err != nil {
		cancel()
		return nil, "", 0, err
	}
	if info.Size != size {
		cancel()
		_ = reader.Close()
		return nil, "", 0, storage.ErrIntegrity
	}
	check := func(ctx context.Context) error {
		return s.userTx(ctx, func(context.Context, pgx.Tx) error { return nil })
	}
	if err = check(streamContext); err != nil {
		cancel()
		_ = reader.Close()
		return nil, "", 0, err
	}
	return newGuardReadCloser(streamContext, cancel, reader, 3*time.Second, check), name, size, nil
}

// SyncPublished writes fresh local receipts after StartSync. It can execute when
// the target is not a member; the cluster admission barrier checks the resulting
// receipts and publication generation before promotion.
func (s *Service) SyncPublished(ctx context.Context, target cluster.Node, plan cluster.RecoveryPlan) error {
	if target.ID != plan.Node.ID || target.StorageGeneration != plan.Node.StorageGeneration || plan.Node.State != "syncing" {
		return ErrInvalid
	}
	store, err := s.cfg.StorageFor(target)
	if err != nil {
		return err
	}
	for _, file := range plan.Files {
		if file.FreshReceipt {
			continue
		}
		sources, err := s.finalSources(ctx, file.OperationID)
		if err != nil {
			return err
		}
		var copied bool
		for _, source := range sources {
			origin, err := s.cfg.StorageFor(source.Node)
			if err != nil {
				continue
			}
			reader, _, err := origin.Open(ctx, source.Key, source.Version)
			if err != nil {
				continue
			}
			var sum [32]byte
			copy(sum[:], file.SHA256)
			receipt, writeErr := store.Write(ctx, "objects/"+file.OperationID, file.SizeBytes, sum, reader)
			_ = reader.Close()
			if writeErr != nil {
				continue
			}
			err = databaseSyncReceipt(ctx, s.cfg.Pool, file.OperationID, target, receipt)
			if err != nil {
				return err
			}
			copied = true
			break
		}
		if !copied {
			return ErrUnavailable
		}
	}
	return nil
}

func databaseSyncReceipt(ctx context.Context, pool *pgxpool.Pool, id string, node cluster.Node, receipt storage.Receipt) error {
	return database.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var version int64
		if err := tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&version); err != nil {
			return err
		}
		var published bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM files WHERE operation_id=$1)", id).Scan(&published); err != nil {
			return err
		}
		if !published {
			return ErrNotFound
		}
		return saveFinalReceipt(ctx, tx, id, node, receipt, true)
	})
}
func (s *Service) finalSources(ctx context.Context, id string) ([]copyLocation, error) {
	rows, err := s.cfg.Pool.Query(ctx, `SELECT n.id::STRING,n.node_id,n.backend_endpoint,n.database_endpoint,n.storage_endpoint,n.storage_generation::STRING,n.state,c.object_key,c.s3_version_id,c.size_bytes,c.sha256
 FROM object_copies c JOIN cluster_nodes n ON n.id=c.node_id AND n.storage_generation=c.storage_generation
 WHERE c.operation_id=$1 AND n.state<>'removed' ORDER BY n.node_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	copies := []copyLocation{}
	for rows.Next() {
		var c copyLocation
		if err := scanCopy(rows, &c); err != nil {
			return nil, err
		}
		copies = append(copies, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	nodes, err := s.cfg.Cluster.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	result := append([]copyLocation{}, copies...)
	seen := map[string]bool{}
	for _, c := range copies {
		seen[c.Node.ID+"/"+c.Key+"/"+c.Version] = true
	}
	for _, c := range copies {
		for _, n := range nodes {
			key := n.ID + "/" + c.Key + "/" + c.Version
			if n.State == "removed" || seen[key] {
				continue
			}
			seen[key] = true
			candidate := c
			candidate.Node = n
			result = append(result, candidate)
		}
	}
	return result, nil
}
