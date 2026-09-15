// Package lifecycle coordinates durable topology operations. External actions are
// reconciled and never executed inside a retryable SQL transaction. Provisioning
// processes and deleting volumes are deliberately outside this package.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBlocked = errors.New("topology operation blocked")
var ErrTopology = errors.New("Cockroach replica placement or topology prevents retirement")
var ErrConflict = errors.New("topology operation conflicts with existing intent")

// Identity contains references only. Credential values remain in adapter config.
type Identity struct {
	ClusterID                                                  string
	SQLNodeID                                                  int64
	DeploymentID, SiteName, StorageEndpoint, CredentialProfile string
}
type Request struct {
	Identity          Identity
	Peers             []Identity
	PreviousIdentity  *Identity
	StorageGeneration string
}
type Operation struct {
	ID, NodeID, Kind, Stage string
	Request                 Request
	ManagerTerm             int64
	LastError               *string
}

// Adapter methods must inspect actual topology before repeating a mutation. An
// accepted remote action may outlive manager authority or the request context.
type Adapter interface {
	Preflight(context.Context, string, Request) error
	SQLRemoved(context.Context, Identity) (bool, error)
	RemoveSQL(context.Context, Identity) error
	StorageMatches(context.Context, string, Request) (bool, error)
	ChangeStorage(context.Context, string, Request) error
}
type Service struct {
	Pool    *pgxpool.Pool
	Adapter Adapter
	stepMu  sync.Mutex
}

func authority(ctx context.Context, tx pgx.Tx, a cluster.Lease) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(holder_id=$1 AND term=$2 AND expires_at>clock_timestamp(),false) FROM manager_lease WHERE singleton=true FOR UPDATE`, a.HolderID, a.Term).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return cluster.ErrNoAuthority
	}
	return nil
}
func lock(ctx context.Context, tx pgx.Tx, a cluster.Lease) error {
	if err := authority(ctx, tx, a); err != nil {
		return err
	}
	var v int64
	return tx.QueryRow(ctx, `SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE`).Scan(&v)
}
func valid(r Request) bool {
	return r.Identity.ClusterID != "" && r.Identity.SQLNodeID > 0 && r.Identity.DeploymentID != "" && r.Identity.SiteName != "" && r.Identity.CredentialProfile != ""
}
func scan(row pgx.Row) (Operation, error) {
	var o Operation
	var b []byte
	err := row.Scan(&o.ID, &o.NodeID, &o.Kind, &o.Stage, &b, &o.ManagerTerm, &o.LastError)
	if err == nil {
		err = json.Unmarshal(b, &o.Request)
	}
	return o, err
}

const columns = `id::STRING,node_id::STRING,kind,stage,request,manager_term,last_error`

func (s *Service) Get(ctx context.Context, id string) (Operation, error) {
	return scan(s.Pool.QueryRow(ctx, `SELECT `+columns+` FROM node_lifecycle_operations WHERE id=$1`, id))
}
func (s *Service) Pending(ctx context.Context) (Operation, error) {
	return scan(s.Pool.QueryRow(ctx, `SELECT `+columns+` FROM node_lifecycle_operations WHERE stage NOT IN ('complete','cancelled') LIMIT 1`))
}

// BeginJoin creates the registry and gate atomically; do not Register first. The
// initial preprovisioned development nodes continue to use cluster.Register.
func (s *Service) BeginJoin(ctx context.Context, a cluster.Lease, r cluster.Registration, req Request, key string) (Operation, error) {
	if !valid(req) || key == "" || r.StorageEndpoint != req.Identity.StorageEndpoint {
		return Operation{}, cluster.ErrInvalid
	}
	// Reuse the registry's public validation without touching its storage.
	if err := cluster.ValidateRegistration(r); err != nil {
		return Operation{}, err
	}
	return s.begin(ctx, a, "join", "", r, req, key)
}
func (s *Service) BeginRetire(ctx context.Context, a cluster.Lease, node string, req Request, key string) (Operation, error) {
	if !valid(req) || key == "" || node == "" {
		return Operation{}, cluster.ErrInvalid
	}
	_, replayErr := scan(s.Pool.QueryRow(ctx, `SELECT `+columns+` FROM node_lifecycle_operations WHERE idempotency_key=$1`, key))
	if errors.Is(replayErr, pgx.ErrNoRows) {
		if err := s.check(ctx, a); err != nil {
			return Operation{}, err
		}
		if s.Adapter == nil {
			return Operation{}, ErrBlocked
		}
		if err := s.Adapter.Preflight(ctx, "retire", req); err != nil {
			return Operation{}, fmt.Errorf("%w: %w", ErrBlocked, err)
		}
	} else if replayErr != nil {
		return Operation{}, replayErr
	}
	return s.begin(ctx, a, "retire", node, cluster.Registration{}, req, key)
}
func (s *Service) begin(ctx context.Context, a cluster.Lease, kind, node string, r cluster.Registration, req Request, key string) (Operation, error) {
	var result Operation
	b, _ := json.Marshal(req)
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, a); err != nil {
			return err
		}
		old, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM node_lifecycle_operations WHERE idempotency_key=$1`, key))
		if err == nil {
			ob, _ := json.Marshal(old.Request)
			if old.Kind != kind || string(ob) != string(b) || (node != "" && old.NodeID != node) {
				return ErrConflict
			}
			if kind == "join" {
				var match bool
				if err := tx.QueryRow(ctx, `SELECT node_id=$2 AND backend_endpoint=$3 AND database_endpoint=$4 AND storage_endpoint=$5 FROM cluster_nodes WHERE id=$1`, old.NodeID, r.NodeID, r.BackendEndpoint, r.DatabaseEndpoint, r.StorageEndpoint).Scan(&match); err != nil {
					return err
				}
				if !match {
					return ErrConflict
				}
			}
			result = old
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_lifecycle_operations WHERE stage NOT IN ('complete','cancelled'))`).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrConflict
		}
		if kind == "join" {
			err = tx.QueryRow(ctx, `INSERT INTO cluster_nodes(node_id,backend_endpoint,database_endpoint,storage_endpoint) VALUES($1,$2,$3,$4) ON CONFLICT(node_id) DO NOTHING RETURNING id::STRING`, r.NodeID, r.BackendEndpoint, r.DatabaseEndpoint, r.StorageEndpoint).Scan(&node)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
		} else {
			var state, endpoint string
			if err := tx.QueryRow(ctx, `SELECT state,storage_endpoint FROM cluster_nodes WHERE id=$1`, node).Scan(&state, &endpoint); err != nil {
				return err
			}
			if state == "removed" || endpoint != req.Identity.StorageEndpoint {
				return ErrConflict
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO node_infrastructure(node_id,identity,blocked) VALUES($1,$2,$3) ON CONFLICT(node_id) DO NOTHING`, node, mustJSON(req.Identity), kind == "join"); err != nil {
			return err
		}
		var same bool
		if err := tx.QueryRow(ctx, `SELECT identity=$2::JSONB FROM node_infrastructure WHERE node_id=$1`, node, mustJSON(req.Identity)).Scan(&same); err != nil {
			return err
		}
		if !same {
			return ErrConflict
		}
		result, err = scan(tx.QueryRow(ctx, `INSERT INTO node_lifecycle_operations(idempotency_key,node_id,kind,stage,request,manager_term) VALUES($1,$2,$3,'preflight',$4,$5) RETURNING `+columns, key, node, kind, b, a.Term))
		if err != nil {
			return err
		}
		return authority(ctx, tx, a)
	})
	return result, err
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// Step advances at most one phase. Run in a worker separate from manager renewal.
// The durable global slot remains occupied on errors, including ambiguous ones.
func (s *Service) Step(ctx context.Context, a cluster.Lease, id string) error {
	s.stepMu.Lock()
	defer s.stepMu.Unlock()
	var o Operation
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, a); err != nil {
			return err
		}
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_infrastructure WHERE node_id=$1 AND blocked)`, a.HolderID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return cluster.ErrNoAuthority
		}
		var err error
		o, err = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM node_lifecycle_operations WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		o, err = joinReplacement(ctx, tx, o)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE node_lifecycle_operations SET manager_term=$2 WHERE id=$1`, id, a.Term)
		return err
	})
	if err != nil {
		return err
	}
	if o.Stage == "complete" || o.Stage == "cancelled" {
		return nil
	}
	if s.Adapter == nil {
		return ErrBlocked
	}
	next := o.Stage
	switch o.Stage {
	case "remove_old":
		replacement, ok := s.Adapter.(ReplacementAdapter)
		if !ok {
			err = ErrBlocked
			break
		}
		var done bool
		done, err = replacement.OldStorageAbsent(ctx, o.Request)
		if err == nil && !done {
			err = s.check(ctx, a)
			if err == nil {
				err = replacement.RemoveOldStorage(ctx, o.Request)
			}
		}
		if err == nil {
			done, err = replacement.OldStorageAbsent(ctx, o.Request)
			if err == nil && done {
				next = "storage"
			}
		}

	case "preflight":
		err = s.Adapter.Preflight(ctx, o.Kind, o.Request)
		if err == nil {
			if o.Kind == "join" {
				next = "storage"
			} else {
				next = "sql"
			}
		}
	case "sql":
		var done bool
		done, err = s.Adapter.SQLRemoved(ctx, o.Request.Identity)
		if err == nil && !done {
			err = s.check(ctx, a)
			if err == nil {
				err = s.Adapter.RemoveSQL(ctx, o.Request.Identity)
			}
		}
		if err == nil {
			done, err = s.Adapter.SQLRemoved(ctx, o.Request.Identity)
			if err == nil && done {
				next = "storage"
			}
		}
	case "storage":
		storageKind := o.Kind
		if storageKind == "replace" {
			storageKind = "join"
		}
		var done bool
		done, err = s.Adapter.StorageMatches(ctx, storageKind, o.Request)
		if err == nil && !done {
			err = s.check(ctx, a)
			if err == nil {
				err = s.Adapter.ChangeStorage(ctx, storageKind, o.Request)
			}
		}
		if err == nil {
			done, err = s.Adapter.StorageMatches(ctx, storageKind, o.Request)
			if err == nil && done {
				next = "complete"
			}
		}
	}
	actionErr := err
	err = database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, a); err != nil {
			return err
		}
		var current string
		if err := tx.QueryRow(ctx, `SELECT stage FROM node_lifecycle_operations WHERE id=$1 FOR UPDATE`, id).Scan(&current); err != nil {
			return err
		}
		if current != o.Stage {
			return ErrConflict
		}
		if err := executionMatches(ctx, tx, o); err != nil {
			return err
		}
		if actionErr == nil && o.Kind == "retire" && o.Stage == "preflight" {
			var survivors int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM cluster_membership WHERE node_id!=$1`, o.NodeID).Scan(&survivors); err != nil {
				return err
			}
			if survivors < 1 {
				return ErrBlocked
			}
			var missing bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM files f JOIN upload_operations u ON u.id=f.operation_id CROSS JOIN cluster_membership m JOIN cluster_nodes n ON n.id=m.node_id WHERE n.id!=$1 AND NOT EXISTS(SELECT 1 FROM object_copies c WHERE c.operation_id=u.id AND c.node_id=n.id AND c.storage_generation=n.storage_generation AND c.size_bytes=u.size_bytes AND c.sha256=u.sha256))`, o.NodeID).Scan(&missing); err != nil {
				return err
			}
			if missing {
				return ErrBlocked
			}
			if _, err := tx.Exec(ctx, `UPDATE node_infrastructure SET blocked=true WHERE node_id=$1`, o.NodeID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM cluster_membership WHERE node_id=$1`, o.NodeID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE cluster_nodes SET state='unavailable',reason='permanent retirement',transitioned_at=clock_timestamp() WHERE id=$1`, o.NodeID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE cluster_configuration SET version=version+1 WHERE singleton=true`); err != nil {
				return err
			}
		}
		if actionErr == nil && next == "complete" {
			if o.Kind == "replace" || (o.Kind == "join" && o.Request.PreviousIdentity != nil) {
				err = finishReplacement(ctx, tx, o)
			} else if o.Kind == "join" {
				var matches bool
				if err = tx.QueryRow(ctx, `SELECT identity=$2::JSONB AND NOT EXISTS(SELECT 1 FROM node_storage_replacements r WHERE r.node_id=$1 AND NOT r.complete) FROM node_infrastructure WHERE node_id=$1`, o.NodeID, mustJSON(o.Request.Identity)).Scan(&matches); err != nil {
					return err
				}
				if !matches {
					return ErrConflict
				}
				_, err = tx.Exec(ctx, `UPDATE node_infrastructure SET blocked=false WHERE node_id=$1`, o.NodeID)
			} else {
				_, err = tx.Exec(ctx, `UPDATE cluster_nodes SET state='removed',transitioned_at=clock_timestamp() WHERE id=$1`, o.NodeID)
			}
			if err != nil {
				return err
			}
		}
		var code any
		if actionErr != nil {
			code = "remote_step_failed"
			if errors.Is(actionErr, ErrTopology) {
				code = "cockroach_topology_blocked"
			}
			if errors.Is(actionErr, ErrConflict) {
				code = "infrastructure_identity_conflict"
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE node_lifecycle_operations SET stage=$2,last_error=$3,updated_at=clock_timestamp(),manager_term=$4 WHERE id=$1`, id, next, code, a.Term); err != nil {
			return err
		}
		if next != o.Stage {
			_, err = tx.Exec(ctx, `INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind,details) SELECT $1,version,$2,'node_lifecycle',jsonb_build_object('stage',$3::STRING,'operation',$4::STRING) FROM cluster_configuration WHERE singleton=true`, o.NodeID, a.Term, next, id)
			if err != nil {
				return err
			}
		}
		return authority(ctx, tx, a)
	})
	if err != nil {
		return err
	}
	if actionErr != nil {
		return fmt.Errorf("%w: %w", ErrBlocked, actionErr)
	}
	return nil
}

// Run resumes persisted intent after election. Its caller must cancel the context
// upon loss of authority. Only one worker should run per manager.
func (s *Service) Run(ctx context.Context, lease func() cluster.Lease, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o, err := s.Pending(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				o, err = s.BeginPendingReplacement(ctx, lease())
			}
			if err == nil {
				_ = s.Step(ctx, lease(), o.ID)
			}
		}
	}
}

func (s *Service) check(ctx context.Context, a cluster.Lease) error {
	return database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := authority(ctx, tx, a); err != nil {
			return err
		}
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_infrastructure WHERE node_id=$1 AND blocked)`, a.HolderID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return cluster.ErrNoAuthority
		}
		return nil
	})
}

// CancelRetirement releases a topology slot only before any mutating remote
// phase. SQL/storage outcomes may be ambiguous and cannot be cancelled here.
func (s *Service) CancelRetirement(ctx context.Context, a cluster.Lease, id string) error {
	return database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, a); err != nil {
			return err
		}
		o, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM node_lifecycle_operations WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if o.Kind != "retire" || (o.Stage != "preflight" && o.Stage != "cancelled") {
			return ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE node_lifecycle_operations SET stage='cancelled',updated_at=clock_timestamp(),manager_term=$2 WHERE id=$1`, id, a.Term)
		if err != nil {
			return err
		}
		return authority(ctx, tx, a)
	})
}
