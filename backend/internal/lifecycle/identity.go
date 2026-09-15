package lifecycle

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

// IdentityObservation describes a local, authenticated observation. The runtime
// must not probe arbitrary user-supplied endpoints on behalf of this method.
type IdentityObservation struct {
	Changed, Blocked  bool
	StorageGeneration string
}

// ObserveIdentity never grants membership. It may revoke local eligibility even
// without a manager lease, locking configuration before node/infrastructure rows.
// Confirmed identity remains unchanged until the replacement operation completes.
func (s *Service) ObserveIdentity(ctx context.Context, node string, observed Identity) (IdentityObservation, error) {
	var out IdentityObservation
	if node == "" || !valid(Request{Identity: observed}) {
		return out, cluster.ErrInvalid
	}
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		out = IdentityObservation{}
		var version int64
		if err := tx.QueryRow(ctx, `SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE`).Scan(&version); err != nil {
			return err
		}
		var endpoint, state string
		if err := tx.QueryRow(ctx, `SELECT storage_endpoint,state,storage_generation::STRING FROM cluster_nodes WHERE id=$1 FOR UPDATE`, node).Scan(&endpoint, &state, &out.StorageGeneration); err != nil {
			return err
		}
		if state == "removed" || endpoint != observed.StorageEndpoint {
			return ErrConflict
		}
		var tombstoned bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_storage_tombstones WHERE node_id=$1 AND deployment_id=$2)`, node, observed.DeploymentID).Scan(&tombstoned); err != nil {
			return err
		}
		if tombstoned {
			return ErrConflict
		}
		var previousBytes []byte
		err := tx.QueryRow(ctx, `SELECT identity,blocked FROM node_infrastructure WHERE node_id=$1 FOR UPDATE`, node).Scan(&previousBytes, &out.Blocked)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO node_infrastructure(node_id,identity,blocked) VALUES($1,$2,false)`, node, mustJSON(observed))
			return err
		}
		if err != nil {
			return err
		}
		var previous Identity
		if err = json.Unmarshal(previousBytes, &previous); err != nil {
			return err
		}
		if previous == observed {
			return nil
		}
		oldWithoutDeployment, newWithoutDeployment := previous, observed
		oldWithoutDeployment.DeploymentID = ""
		newWithoutDeployment.DeploymentID = ""
		if oldWithoutDeployment != newWithoutDeployment {
			return ErrConflict
		}
		var pending []byte
		err = tx.QueryRow(ctx, `SELECT observed_identity FROM node_storage_replacements WHERE node_id=$1 AND NOT complete FOR UPDATE`, node).Scan(&pending)
		if err == nil {
			var already Identity
			if err = json.Unmarshal(pending, &already); err != nil {
				return err
			}
			if already != observed {
				return ErrConflict
			}
			out.Blocked = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO node_storage_tombstones(node_id,deployment_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, node, previous.DeploymentID); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `UPDATE cluster_nodes SET storage_generation=gen_random_uuid(),state='unavailable',reason='storage volume replaced',transitioned_at=clock_timestamp() WHERE id=$1 RETURNING storage_generation::STRING`, node).Scan(&out.StorageGeneration); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM cluster_membership WHERE node_id=$1`, node); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE node_infrastructure SET blocked=true WHERE node_id=$1`, node); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `UPDATE cluster_configuration SET version=version+1,updated_at=clock_timestamp() WHERE singleton=true RETURNING version`).Scan(&version); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO node_storage_replacements(node_id,previous_identity,observed_identity,storage_generation) VALUES($1,$2,$3,$4) ON CONFLICT(node_id) DO UPDATE SET previous_identity=excluded.previous_identity,observed_identity=excluded.observed_identity,storage_generation=excluded.storage_generation,operation_id=NULL,complete=false,observed_at=clock_timestamp() WHERE node_storage_replacements.complete`, node, previousBytes, mustJSON(observed), out.StorageGeneration); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind,details) VALUES($1,$2,0,'storage_identity_changed',jsonb_build_object('previous_deployment',$3::STRING,'observed_deployment',$4::STRING))`, node, version, previous.DeploymentID, observed.DeploymentID); err != nil {
			return err
		}
		out.Changed = true
		out.Blocked = true
		return nil
	})
	return out, err
}

// BeginPendingReplacement claims one queued observation under manager authority.
// Unrelated topology operations keep their durable global slot until reconciled.
func (s *Service) BeginPendingReplacement(ctx context.Context, a cluster.Lease) (Operation, error) {
	var out Operation
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, a); err != nil {
			return err
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_lifecycle_operations WHERE stage NOT IN ('complete','cancelled'))`).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrConflict
		}
		var node, generation string
		var oldBytes, newBytes []byte
		if err := tx.QueryRow(ctx, `SELECT r.node_id::STRING,r.previous_identity,r.observed_identity,r.storage_generation::STRING FROM node_storage_replacements r JOIN cluster_nodes n ON n.id=r.node_id WHERE NOT r.complete AND r.operation_id IS NULL AND n.state!='removed' ORDER BY r.observed_at LIMIT 1 FOR UPDATE`).Scan(&node, &oldBytes, &newBytes, &generation); err != nil {
			return err
		}
		var req Request
		req.PreviousIdentity = &Identity{}
		if err := json.Unmarshal(oldBytes, req.PreviousIdentity); err != nil {
			return err
		}
		if err := json.Unmarshal(newBytes, &req.Identity); err != nil {
			return err
		}
		req.StorageGeneration = generation
		// MinIO topology includes temporarily unavailable sites, so do not use only
		// application membership. Every retained site requires a confirmed identity.
		rows, err := tx.Query(ctx, `SELECT i.identity FROM cluster_nodes n LEFT JOIN node_infrastructure i ON i.node_id=n.id WHERE n.id!=$1 AND n.state!='removed' ORDER BY n.node_id`, node)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b []byte
			if err = rows.Scan(&b); err != nil {
				rows.Close()
				return err
			}
			if len(b) == 0 {
				rows.Close()
				return ErrBlocked
			}
			var i Identity
			if err = json.Unmarshal(b, &i); err != nil {
				rows.Close()
				return err
			}
			req.Peers = append(req.Peers, i)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = participants(req); err != nil {
			return err
		}
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO node_lifecycle_operations(idempotency_key,node_id,kind,stage,request,manager_term) VALUES($1,$2,'replace','remove_old',$3,$4) RETURNING `+columns, "replace:"+node+":"+generation, node, mustJSON(req), a.Term))
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE node_storage_replacements SET operation_id=$2 WHERE node_id=$1`, node, out.ID); err != nil {
			return err
		}
		return authority(ctx, tx, a)
	})
	return out, err
}

// finishReplacement runs under the same configuration lock as publication. Old
// receipts remain for audit; generation comparisons make them unusable.
func finishReplacement(ctx context.Context, tx pgx.Tx, o Operation) error {
	var matches bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_storage_replacements r JOIN cluster_nodes n ON n.id=r.node_id WHERE r.node_id=$1 AND r.operation_id=$2 AND NOT r.complete AND r.observed_identity=$3::JSONB AND r.storage_generation=$4 AND n.storage_generation=r.storage_generation AND n.state!='removed')`, o.NodeID, o.ID, mustJSON(o.Request.Identity), o.Request.StorageGeneration).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE node_storage_replacements SET complete=true WHERE node_id=$1`, o.NodeID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE node_infrastructure SET identity=$2,blocked=false WHERE node_id=$1`, o.NodeID, mustJSON(o.Request.Identity))
	return err
}

// ReplacementAdapter leaves SQL membership intact and replaces only the site.
// A partial MinIO response is insufficient: every survivor must confirm removal.
type ReplacementAdapter interface {
	OldStorageAbsent(context.Context, Request) (bool, error)
	RemoveOldStorage(context.Context, Request) error
}

// joinReplacement folds recovery into the existing join without changing its
// public request/idempotency key or releasing the global topology slot. Invoke
// only when the worker is between external calls, under lease/config/row locks.
func joinReplacement(ctx context.Context, tx pgx.Tx, o Operation) (Operation, error) {
	if o.Kind != "join" || o.Stage == "complete" || o.Stage == "cancelled" {
		return o, nil
	}
	var oldBytes, newBytes []byte
	var generation string
	var attached *string
	err := tx.QueryRow(ctx, `SELECT previous_identity,observed_identity,storage_generation::STRING,operation_id::STRING FROM node_storage_replacements WHERE node_id=$1 AND NOT complete FOR UPDATE`, o.NodeID).Scan(&oldBytes, &newBytes, &generation, &attached)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	var previous, observed Identity
	if err = json.Unmarshal(oldBytes, &previous); err != nil {
		return o, err
	}
	if err = json.Unmarshal(newBytes, &observed); err != nil {
		return o, err
	}
	effective, err := replacementExecution(o, previous, observed, generation, attached)
	if err != nil {
		return o, err
	}
	if attached == nil {
		if _, err = tx.Exec(ctx, `UPDATE node_storage_replacements SET operation_id=$2 WHERE node_id=$1 AND operation_id IS NULL`, o.NodeID, o.ID); err != nil {
			return o, err
		}
		if _, err = tx.Exec(ctx, `UPDATE node_lifecycle_operations SET stage='remove_old',last_error=NULL,updated_at=clock_timestamp() WHERE id=$1`, o.ID); err != nil {
			return o, err
		}
	}
	return effective, nil
}

// replacementExecution is a private execution view; never persist its Request
// over the original join request or use it for idempotency comparisons.
func replacementExecution(o Operation, previous, observed Identity, generation string, attached *string) (Operation, error) {
	if o.Request.Identity != previous || generation == "" || (attached != nil && *attached != o.ID) {
		return o, ErrConflict
	}
	if attached == nil && o.Stage != "preflight" && o.Stage != "storage" {
		return o, ErrConflict
	}
	req := o.Request
	req.PreviousIdentity = &previous
	req.Identity = observed
	req.StorageGeneration = generation
	if err := replacementIdentities(req); err != nil {
		return o, err
	}
	o.Request = req
	if attached == nil {
		o.Stage = "remove_old"
	}
	return o, nil
}

// executionMatches fences the storage -> remove_old -> storage ABA case. A
// response produced before the replacement was attached cannot complete it.
func executionMatches(ctx context.Context, tx pgx.Tx, o Operation) error {
	if o.Kind != "join" && o.Kind != "replace" {
		return nil
	}
	var generation string
	var attached *string
	var observed []byte
	err := tx.QueryRow(ctx, `SELECT storage_generation::STRING,operation_id::STRING,observed_identity FROM node_storage_replacements WHERE node_id=$1 AND NOT complete`, o.NodeID).Scan(&generation, &attached, &observed)
	if errors.Is(err, pgx.ErrNoRows) {
		if o.Request.PreviousIdentity != nil {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	var identity Identity
	if err = json.Unmarshal(observed, &identity); err != nil {
		return err
	}
	if !sameExecution(o, generation, attached, identity) {
		return ErrConflict
	}
	return nil
}
func sameExecution(o Operation, generation string, attached *string, observed Identity) bool {
	return attached != nil && *attached == o.ID && o.Request.PreviousIdentity != nil && o.Request.StorageGeneration == generation && o.Request.Identity == observed
}
