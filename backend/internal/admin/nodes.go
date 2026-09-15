package admin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/lifecycle"
	"github.com/jackc/pgx/v5"
)

type JoinInput struct {
	Key               string `json:"idempotency_key"`
	NodeID            string `json:"node_id"`
	BackendEndpoint   string `json:"backend_endpoint"`
	DatabaseEndpoint  string `json:"database_endpoint"`
	StorageEndpoint   string `json:"storage_endpoint"`
	CredentialProfile string `json:"credential_profile"`
}

// NodeManager accepts operator intent. The elected manager alone runs the
// durable external steps. Discover checks the preprovisioned node's identity.
type NodeManager struct {
	Lifecycle *lifecycle.Service
	Registry  *cluster.Store
	Discover  func(context.Context, cluster.Registration, string) (lifecycle.Identity, error)
}

// NodeOperation deliberately omits credential profiles and internal endpoints.
type NodeOperation struct {
	Key         string  `json:"idempotency_key,omitempty"`
	ID          string  `json:"id"`
	NodeID      string  `json:"node_id"`
	Kind        string  `json:"kind"`
	Stage       string  `json:"stage"`
	ManagerTerm int64   `json:"manager_term"`
	LastError   *string `json:"last_error"`
}

func operationView(o lifecycle.Operation) NodeOperation {
	return NodeOperation{ID: o.ID, NodeID: o.NodeID, Kind: o.Kind, Stage: o.Stage, ManagerTerm: o.ManagerTerm, LastError: o.LastError}
}

func (m *NodeManager) authority(ctx context.Context) (cluster.Lease, error) {
	var a cluster.Lease
	err := m.Lifecycle.Pool.QueryRow(ctx, `SELECT holder_id::STRING,term,expires_at FROM manager_lease WHERE singleton=true AND expires_at>clock_timestamp() AND holder_id IS NOT NULL`).Scan(&a.HolderID, &a.Term, &a.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = cluster.ErrNoAuthority
	}
	return a, err
}

func (m *NodeManager) previous(ctx context.Context, key string) (lifecycle.Operation, error) {
	var id string
	if err := m.Lifecycle.Pool.QueryRow(ctx, "SELECT id::STRING FROM node_lifecycle_operations WHERE idempotency_key=$1", key).Scan(&id); err != nil {
		return lifecycle.Operation{}, err
	}
	return m.Lifecycle.Get(ctx, id)
}

func (m *NodeManager) identity(ctx context.Context, n cluster.Node) (lifecycle.Identity, error) {
	var identity lifecycle.Identity
	var raw []byte
	err := m.Lifecycle.Pool.QueryRow(ctx, "SELECT identity FROM node_infrastructure WHERE node_id=$1", n.ID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &identity)
		return identity, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identity, err
	}
	return m.Discover(ctx, n.Registration, "default")
}

func (m *NodeManager) peers(ctx context.Context, target string) ([]lifecycle.Identity, error) {
	nodes, err := m.Registry.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	peers := []lifecycle.Identity{}
	for _, n := range nodes {
		if n.ID == target || n.State == "removed" {
			continue
		}
		identity, err := m.identity(ctx, n)
		if err != nil {
			return nil, err
		}
		peers = append(peers, identity)
	}
	return peers, nil
}

func (m *NodeManager) Join(ctx context.Context, input JoinInput) (NodeOperation, error) {
	r := cluster.Registration{NodeID: input.NodeID, BackendEndpoint: input.BackendEndpoint, DatabaseEndpoint: input.DatabaseEndpoint, StorageEndpoint: input.StorageEndpoint}
	if err := cluster.ValidateRegistration(r); err != nil {
		return NodeOperation{}, err
	}
	if input.Key == "" || len(input.Key) > 128 {
		return NodeOperation{}, cluster.ErrInvalid
	}
	if input.CredentialProfile == "" {
		input.CredentialProfile = "default"
	}
	var req lifecycle.Request
	old, err := m.previous(ctx, input.Key)
	if err == nil {
		if old.Kind != "join" || old.Request.Identity.CredentialProfile != input.CredentialProfile {
			return NodeOperation{}, lifecycle.ErrConflict
		}
		req = old.Request
	} else if errors.Is(err, pgx.ErrNoRows) {
		req.Identity, err = m.Discover(ctx, r, input.CredentialProfile)
		if err != nil {
			return NodeOperation{}, err
		}
		req.Peers, err = m.peers(ctx, "")
		if err != nil {
			return NodeOperation{}, err
		}
	} else {
		return NodeOperation{}, err
	}
	a, err := m.authority(ctx)
	if err != nil {
		return NodeOperation{}, err
	}
	op, err := m.Lifecycle.BeginJoin(ctx, a, r, req, input.Key)
	view := operationView(op)
	view.Key = input.Key
	return view, err
}

func (m *NodeManager) Retire(ctx context.Context, nodeID, key string) (NodeOperation, error) {
	if key == "" || len(key) > 128 {
		return NodeOperation{}, cluster.ErrInvalid
	}
	var req lifecycle.Request
	old, err := m.previous(ctx, key)
	if err == nil {
		if old.Kind != "retire" || old.NodeID != nodeID {
			return NodeOperation{}, lifecycle.ErrConflict
		}
		req = old.Request
	} else if errors.Is(err, pgx.ErrNoRows) {
		nodes, err := m.Registry.Nodes(ctx)
		if err != nil {
			return NodeOperation{}, err
		}
		found := false
		for _, n := range nodes {
			if n.ID != nodeID {
				continue
			}
			found = true
			req.Identity, err = m.identity(ctx, n)
			if err != nil {
				return NodeOperation{}, err
			}
		}
		if !found {
			return NodeOperation{}, cluster.ErrNotFound
		}
		req.Peers, err = m.peers(ctx, nodeID)
		if err != nil {
			return NodeOperation{}, err
		}
	} else {
		return NodeOperation{}, err
	}
	a, err := m.authority(ctx)
	if err != nil {
		return NodeOperation{}, err
	}
	op, err := m.Lifecycle.BeginRetire(ctx, a, nodeID, req, key)
	view := operationView(op)
	view.Key = key
	return view, err
}

func (m *NodeManager) Operations(ctx context.Context) ([]NodeOperation, error) {
	rows, err := m.Lifecycle.Pool.Query(ctx, `SELECT id::STRING,node_id::STRING,kind,stage,manager_term,last_error,idempotency_key FROM node_lifecycle_operations ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NodeOperation{}
	for rows.Next() {
		var o NodeOperation
		if err := rows.Scan(&o.ID, &o.NodeID, &o.Kind, &o.Stage, &o.ManagerTerm, &o.LastError, &o.Key); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

func (m *NodeManager) CancelRetirement(ctx context.Context, id string) (NodeOperation, error) {
	a, err := m.authority(ctx)
	if err != nil {
		return NodeOperation{}, err
	}
	if err = m.Lifecycle.CancelRetirement(ctx, a, id); err != nil {
		return NodeOperation{}, err
	}
	op, err := m.Lifecycle.Get(ctx, id)
	return operationView(op), err
}

func (m *NodeManager) Operation(ctx context.Context, id string) (NodeOperation, error) {
	op, err := m.Lifecycle.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = cluster.ErrNotFound
	}
	if err != nil {
		return NodeOperation{}, err
	}
	view := operationView(op)
	err = m.Lifecycle.Pool.QueryRow(ctx, "SELECT idempotency_key FROM node_lifecycle_operations WHERE id=$1", id).Scan(&view.Key)
	return view, err
}
