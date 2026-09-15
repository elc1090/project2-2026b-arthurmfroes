// Package uploads owns durable transfer operations and their verified copies.
package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
)

var (
	ErrInvalid     = errors.New("invalid upload")
	ErrNotFound    = errors.New("upload or file not found")
	ErrConflict    = errors.New("upload conflicts with its immutable content or state")
	ErrUnavailable = errors.New("upload content is not currently recoverable")
)

const MaxPartSize int64 = 32 << 20
const maxSafeInteger int64 = 1<<53 - 1

type PartInput struct {
	Index  int64  `json:"index"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Part struct {
	PartInput
	Available    bool   `json:"available"`
	Availability string `json:"availability"`
}
type CreateInput struct {
	IdempotencyKey string      `json:"idempotency_key"`
	FolderID       *string     `json:"folder_id"`
	Name           string      `json:"name"`
	Size           int64       `json:"size"`
	SHA256         string      `json:"sha256"`
	Parts          []PartInput `json:"parts"`
}
type Operation struct {
	ID             string  `json:"id"`
	IdempotencyKey string  `json:"idempotency_key"`
	FolderID       *string `json:"folder_id"`
	Name           string  `json:"name"`
	Size           int64   `json:"size"`
	SHA256         string  `json:"sha256"`
	Parts          []Part  `json:"parts"`
	Status         string  `json:"status"`
	Phase          string  `json:"phase"`
	FileID         *string `json:"file_id,omitempty"`
	ErrorCode      *string `json:"error_code,omitempty"`
}
type ServiceConfig struct {
	Pool         *pgxpool.Pool
	LocalNode    cluster.Node
	LocalStorage *storage.Store
	Cluster      *cluster.Store
	StorageFor   func(cluster.Node) (*storage.Store, error)
	Eligible     func(context.Context) error
}
type Service struct{ cfg ServiceConfig }

func New(cfg ServiceConfig) (*Service, error) {
	if cfg.Pool == nil || cfg.LocalStorage == nil || cfg.Cluster == nil || cfg.LocalNode.ID == "" || cfg.LocalNode.StorageGeneration == "" || cfg.StorageFor == nil || cfg.Eligible == nil {
		return nil, ErrInvalid
	}
	return &Service{cfg: cfg}, nil
}

func validUUID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return err == nil
}
func digest(s string) ([32]byte, error) {
	var sum [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return sum, ErrInvalid
	}
	copy(sum[:], b)
	return sum, nil
}
func normalize(input CreateInput) (CreateInput, error) {
	if !validUUID(input.IdempotencyKey) || !catalog.ValidName(input.Name) || input.Size < 0 || input.Size > maxSafeInteger || (input.FolderID != nil && !validUUID(*input.FolderID)) {
		return input, ErrInvalid
	}
	sum, err := digest(input.SHA256)
	if err != nil {
		return input, err
	}
	input.SHA256 = hex.EncodeToString(sum[:])
	input.IdempotencyKey = strings.ToLower(input.IdempotencyKey)
	if input.FolderID != nil {
		folder := strings.ToLower(*input.FolderID)
		input.FolderID = &folder
	}
	input.Parts = append([]PartInput{}, input.Parts...)
	var offset int64
	for i := range input.Parts {
		p := &input.Parts[i]
		if p.Index != int64(i) || p.Offset != offset || p.Size <= 0 || p.Size > MaxPartSize || p.Size > input.Size-offset {
			return input, ErrInvalid
		}
		hash, err := digest(p.SHA256)
		if err != nil {
			return input, err
		}
		p.SHA256 = hex.EncodeToString(hash[:])
		offset += p.Size
	}
	if offset != input.Size || (input.Size == 0 && sum != sha256.Sum256(nil)) {
		return input, ErrInvalid
	}
	return input, nil
}
func mapError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, catalog.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, catalog.ErrConflict) {
		return ErrConflict
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "22P02" {
			return ErrInvalid
		}
		if pg.Code == "23505" {
			return ErrConflict
		}
	}
	return err
}
func (s *Service) guard(ctx context.Context, tx pgx.Tx) error {
	return cluster.Guard(ctx, tx, s.cfg.LocalNode.ID)
}
func (s *Service) userTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.cfg.Eligible(ctx); err != nil {
		return err
	}
	return mapError(database.WithTx(ctx, s.cfg.Pool, func(tx pgx.Tx) error {
		if err := s.guard(ctx, tx); err != nil {
			return err
		}
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return s.guard(ctx, tx)
	}))
}

func (s *Service) Create(ctx context.Context, owner string, input CreateInput) (Operation, error) {
	input, err := normalize(input)
	if err != nil {
		return Operation{}, err
	}
	manifest, _ := json.Marshal(input.Parts)
	sum, _ := digest(input.SHA256)
	var id string
	err = s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "SELECT id::STRING FROM upload_operations WHERE owner_id=$1 AND idempotency_key=$2", owner, input.IdempotencyKey).Scan(&id)
		if err == nil {
			return matchInput(ctx, tx, owner, id, input)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := catalog.LockDirectory(ctx, tx, owner, input.FolderID); err != nil {
			return err
		}
		if err := catalog.CheckName(ctx, tx, owner, input.FolderID, input.Name); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO upload_operations(owner_id,folder_id,name,idempotency_key,size_bytes,sha256,manifest,part_count)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(owner_id,idempotency_key) DO NOTHING RETURNING id::STRING`, owner, input.FolderID, input.Name, input.IdempotencyKey, input.Size, sum[:], manifest, len(input.Parts)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			if err = tx.QueryRow(ctx, "SELECT id::STRING FROM upload_operations WHERE owner_id=$1 AND idempotency_key=$2", owner, input.IdempotencyKey).Scan(&id); err != nil {
				return err
			}
			return matchInput(ctx, tx, owner, id, input)
		}
		if err != nil {
			return err
		}
		for _, p := range input.Parts {
			hash, _ := digest(p.SHA256)
			if _, err = tx.Exec(ctx, "INSERT INTO upload_parts(operation_id,part_index,offset_bytes,size_bytes,sha256) VALUES($1,$2,$3,$4,$5)", id, p.Index, p.Offset, p.Size, hash[:]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Operation{}, err
	}
	return s.Get(ctx, owner, id)
}
func matchInput(ctx context.Context, tx pgx.Tx, owner, id string, input CreateInput) error {
	op, err := loadOperation(ctx, tx, owner, id, false)
	if err != nil {
		return err
	}
	folderEqual := (op.FolderID == nil && input.FolderID == nil) || (op.FolderID != nil && input.FolderID != nil && *op.FolderID == *input.FolderID)
	if !folderEqual || op.Name != input.Name || op.Size != input.Size || op.SHA256 != input.SHA256 || len(op.Parts) != len(input.Parts) {
		return ErrConflict
	}
	for i := range op.Parts {
		if op.Parts[i].PartInput != input.Parts[i] {
			return ErrConflict
		}
	}
	return nil
}
func loadOperation(ctx context.Context, tx pgx.Tx, owner, id string, lock bool) (Operation, error) {
	op := Operation{Parts: []Part{}}
	var raw []byte
	var manifest []byte
	query := `SELECT id::STRING,idempotency_key::STRING,folder_id::STRING,name,size_bytes,sha256,manifest,status,phase,error_code FROM upload_operations WHERE id=$1 AND owner_id=$2`
	if lock {
		query += " FOR UPDATE"
	}
	if err := tx.QueryRow(ctx, query, id, owner).Scan(&op.ID, &op.IdempotencyKey, &op.FolderID, &op.Name, &op.Size, &raw, &manifest, &op.Status, &op.Phase, &op.ErrorCode); err != nil {
		return op, err
	}
	op.SHA256 = hex.EncodeToString(raw)
	var parts []PartInput
	if err := json.Unmarshal(manifest, &parts); err != nil {
		return op, err
	}
	for _, p := range parts {
		op.Parts = append(op.Parts, Part{PartInput: p, Availability: "unknown"})
	}
	if op.Status == "available" {
		if err := tx.QueryRow(ctx, "SELECT id::STRING FROM files WHERE operation_id=$1 AND owner_id=$2", id, owner).Scan(&op.FileID); err != nil {
			return op, err
		}
	}
	return op, nil
}
func (s *Service) Get(ctx context.Context, owner, id string) (Operation, error) {
	var op Operation
	err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		op, err = loadOperation(ctx, tx, owner, id, false)
		return err
	})
	if err != nil {
		return Operation{}, err
	}
	if op.Status == "pending" {
		for i := range op.Parts {
			available, state, err := s.partAvailable(ctx, op.ID, op.Parts[i].Index)
			if err != nil {
				return Operation{}, err
			}
			op.Parts[i].Available = available
			op.Parts[i].Availability = state
		}
	}
	if err = s.cfg.Eligible(ctx); err != nil {
		return Operation{}, err
	}
	return op, nil
}
func (s *Service) List(ctx context.Context, owner string) ([]Operation, error) {
	ids := []string{}
	err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ids = ids[:0]
		rows, err := tx.Query(ctx, "SELECT id::STRING FROM upload_operations WHERE owner_id=$1 ORDER BY created_at,id", owner)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := []Operation{}
	for _, id := range ids {
		op, err := s.Get(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, nil
}
func (s *Service) PutPart(ctx context.Context, owner, id string, index int64, reader io.Reader) error {
	var op Operation
	if err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		op, err = loadOperation(ctx, tx, owner, id, false)
		return err
	}); err != nil {
		return err
	}
	if op.Status != "pending" {
		return ErrConflict
	}
	if index < 0 || index >= int64(len(op.Parts)) {
		return ErrInvalid
	}
	part := op.Parts[index]
	sum, _ := digest(part.SHA256)
	receipt, err := s.cfg.LocalStorage.Write(ctx, fmt.Sprintf("parts/%s/%d", id, index), part.Size, sum, reader)
	if err != nil {
		return err
	}
	return s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadOperation(ctx, tx, owner, id, true)
		if err != nil {
			return err
		}
		if current.Status != "pending" {
			return ErrConflict
		}
		var generation string
		if err = tx.QueryRow(ctx, "SELECT storage_generation::STRING FROM cluster_nodes WHERE id=$1", s.cfg.LocalNode.ID).Scan(&generation); err != nil {
			return err
		}
		if generation != s.cfg.LocalNode.StorageGeneration {
			return ErrUnavailable
		}
		_, err = tx.Exec(ctx, `INSERT INTO upload_part_copies(operation_id,part_index,node_id,storage_generation,object_key,s3_version_id,size_bytes,sha256,verified_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()) ON CONFLICT(operation_id,part_index,node_id,storage_generation)
   DO UPDATE SET object_key=excluded.object_key,s3_version_id=excluded.s3_version_id,verified_at=excluded.verified_at`, id, index, s.cfg.LocalNode.ID, generation, receipt.Key, receipt.VersionID, receipt.Size, receipt.SHA256[:])
		return err
	})
}
func (s *Service) Cancel(ctx context.Context, owner, id string) (Operation, error) {
	err := s.userTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		op, err := loadOperation(ctx, tx, owner, id, true)
		if err != nil {
			return err
		}
		if op.Status != "pending" {
			return nil
		}
		_, err = tx.Exec(ctx, "UPDATE upload_operations SET status='cancelled',phase='cancelled',lease_holder=NULL,lease_expires_at=NULL,lease_generation=lease_generation+1,updated_at=clock_timestamp() WHERE id=$1", id)
		return err
	})
	if err != nil {
		return Operation{}, err
	}
	return s.Get(ctx, owner, id)
}

type copyLocation struct {
	Node         cluster.Node
	Key, Version string
	Size         int64
	SHA256       []byte
}

func (s *Service) partCopies(ctx context.Context, id string, index int64) ([]copyLocation, error) {
	rows, err := s.cfg.Pool.Query(ctx, `SELECT n.id::STRING,n.node_id,n.backend_endpoint,n.database_endpoint,n.storage_endpoint,c.storage_generation::STRING,n.state,c.object_key,c.s3_version_id,c.size_bytes,c.sha256
 FROM upload_part_copies c JOIN cluster_nodes n ON n.id=c.node_id
 WHERE c.operation_id=$1 AND c.part_index=$2 ORDER BY n.node_id`, id, index)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []copyLocation{}
	for rows.Next() {
		var c copyLocation
		if err := scanCopy(rows, &c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func scanCopy(row pgx.Row, c *copyLocation) error {
	return row.Scan(&c.Node.ID, &c.Node.NodeID, &c.Node.BackendEndpoint, &c.Node.DatabaseEndpoint, &c.Node.StorageEndpoint, &c.Node.StorageGeneration, &c.Node.State, &c.Key, &c.Version, &c.Size, &c.SHA256)
}
func (s *Service) partAvailable(ctx context.Context, id string, index int64) (bool, string, error) {
	copies, err := s.partSources(ctx, id, index)
	if err != nil {
		return false, "unknown", err
	}
	unknown := false
	for _, copy := range copies {
		store, err := s.cfg.StorageFor(copy.Node)
		if err != nil {
			unknown = true
			continue
		}
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		reader, info, err := store.Open(probe, copy.Key, copy.Version)
		if err == nil {
			_ = reader.Close()
			cancel()
			if info.Size == copy.Size {
				return true, "available", nil
			}
			continue
		}
		cancel()
		var response minio.ErrorResponse
		if errors.As(err, &response) && response.StatusCode == 404 {
			continue
		}
		// A failed request is unknown rather than proof of loss. A later retry may
		// recover this copy; never tell the browser to discard it because of a timeout.
		unknown = true
	}
	if unknown {
		return false, "unknown", nil
	}
	return false, "missing", nil
}

// partSources treats historical versions as byte sources, never current local
// receipts. A replaced origin is excluded, while other peers may retain bytes.
func (s *Service) partSources(ctx context.Context, id string, index int64) ([]copyLocation, error) {
	copies, err := s.partCopies(ctx, id, index)
	if err != nil || len(copies) == 0 {
		return copies, err
	}
	nodes, err := s.cfg.Cluster.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	var result []copyLocation
	seen := map[string]bool{}
	add := func(c copyLocation, n cluster.Node) {
		if n.State == "removed" || (n.ID == c.Node.ID && n.StorageGeneration != c.Node.StorageGeneration) {
			return
		}
		key := n.ID + "/" + c.Key + "/" + c.Version
		if seen[key] {
			return
		}
		seen[key] = true
		c.Node = n
		result = append(result, c)
	}
	for _, c := range copies {
		for _, n := range nodes {
			if n.ID == c.Node.ID {
				add(c, n)
			}
		}
	}
	for _, c := range copies {
		for _, n := range nodes {
			add(c, n)
		}
	}
	return result, nil
}
