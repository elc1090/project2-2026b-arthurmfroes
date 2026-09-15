// Package cluster stores registry, membership and fenced manager leases. It does
// not probe health, copy objects, or start an election loop. Its caller must first
// verify local components before standing for election or admission. Register joins the
// registry; it never grants eligibility for user requests or uploads.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConflict    = errors.New("node identity conflicts with its registered endpoints")
	ErrNoAuthority = errors.New("manager authority is unavailable or expired")
	ErrNotFound    = errors.New("node not found")
	ErrInvalid     = errors.New("invalid cluster input")
)

type Registration struct{ NodeID, BackendEndpoint, DatabaseEndpoint, StorageEndpoint string }
type Node struct {
	ID string
	Registration
	StorageGeneration string
	State             string
}
type Snapshot struct {
	Version, PublicationGeneration int64
	Members                        []Node
}
type Lease struct {
	HolderID  string
	Term      int64
	ExpiresAt time.Time
}
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Register is a trusted control-plane operation. Identical registration supports
// restarts without resetting state or storage generation. Changed endpoints require
// an explicit future administrative workflow, not silent identity replacement.
func (s *Store) Register(ctx context.Context, r Registration) (Node, error) {
	if err := validate(r); err != nil {
		return Node{}, err
	}
	var node Node
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO cluster_nodes(node_id,backend_endpoint,database_endpoint,storage_endpoint)
   VALUES($1,$2,$3,$4) ON CONFLICT(node_id) DO NOTHING
   RETURNING id::STRING,node_id,backend_endpoint,database_endpoint,storage_endpoint,storage_generation::STRING,state`, r.NodeID, r.BackendEndpoint, r.DatabaseEndpoint, r.StorageEndpoint)
		err := scanNode(row, &node)
		if errors.Is(err, pgx.ErrNoRows) {
			err = scanNode(tx.QueryRow(ctx, `SELECT id::STRING,node_id,backend_endpoint,database_endpoint,storage_endpoint,storage_generation::STRING,state FROM cluster_nodes WHERE node_id=$1`, r.NodeID), &node)
		}
		if err != nil {
			return err
		}
		if node.Registration != r {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return Node{}, err
	}
	return node, nil
}

// Snapshot returns one serializable view. An empty set is valid for control, but
// must NEVER be interpreted by publication code as satisfying required copies.
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		result = Snapshot{Members: []Node{}}
		if err := tx.QueryRow(ctx, "SELECT version,publication_generation FROM cluster_configuration WHERE singleton=true").Scan(&result.Version, &result.PublicationGeneration); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT n.id::STRING,n.node_id,n.backend_endpoint,n.database_endpoint,n.storage_endpoint,n.storage_generation::STRING,n.state FROM cluster_membership m JOIN cluster_nodes n ON n.id=m.node_id ORDER BY n.node_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n Node
			if err := scanNode(rows, &n); err != nil {
				return err
			}
			result.Members = append(result.Members, n)
		}
		return rows.Err()
	})
	if err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func scanNode(row pgx.Row, n *Node) error {
	return row.Scan(&n.ID, &n.NodeID, &n.BackendEndpoint, &n.DatabaseEndpoint, &n.StorageEndpoint, &n.StorageGeneration, &n.State)
}

func validate(r Registration) error {
	if strings.TrimSpace(r.NodeID) == "" || strings.ContainsAny(r.NodeID, " \t\r\n") {
		return fmt.Errorf("%w: NODE_ID", ErrInvalid)
	}
	for _, e := range []struct {
		name, raw string
		database  bool
	}{{"backend", r.BackendEndpoint, false}, {"database", r.DatabaseEndpoint, true}, {"storage", r.StorageEndpoint, false}} {
		u, err := url.Parse(e.raw)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
			return fmt.Errorf("%w: %s endpoint", ErrInvalid, e.name)
		}
		if e.database {
			if u.Scheme != "postgres" && u.Scheme != "postgresql" {
				return fmt.Errorf("%w: database scheme", ErrInvalid)
			}
		} else if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%w: %s scheme", ErrInvalid, e.name)
		}
		if u.RawQuery != "" || u.ForceQuery || strings.HasSuffix(u.Host, ":") {
			return fmt.Errorf("%w: %s endpoint parameters", ErrInvalid, e.name)
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("%w: %s port", ErrInvalid, e.name)
			}
		}
	}
	return nil
}

// ValidateRegistration exposes syntax validation for atomic lifecycle registration.
func ValidateRegistration(r Registration) error { return validate(r) }
