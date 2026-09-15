// Package faults persists development-only operation gates. It does not grant
// administrative access, change membership, publish files, or erase node data.
// Callers authenticate/authorize Set separately; internal restoration must remain
// reachable without the normal user eligibility guard.
package faults

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Mode string

const (
	None    Mode = "none"
	Backend Mode = "backend"
	SQL     Mode = "sql"
	Storage Mode = "storage"
	Control Mode = "control"
	Total   Mode = "total"
)

var (
	ErrDisabled = errors.New("fault simulation is disabled")
	ErrInvalid  = errors.New("invalid fault mode or component")
	ErrNotFound = errors.New("fault target node not found")
	ErrInjected = errors.New("operation rejected by simulated node failure")
)

type Service struct {
	Pool    *pgxpool.Pool
	Enabled bool
}

func New(pool *pgxpool.Pool, enabled bool) *Service { return &Service{Pool: pool, Enabled: enabled} }
func valid(mode Mode) bool {
	return mode == None || mode == Backend || mode == SQL || mode == Storage || mode == Control || mode == Total
}

func (s *Service) Set(ctx context.Context, nodeID string, mode Mode) error {
	if !s.Enabled {
		return ErrDisabled
	}
	if !valid(mode) || nodeID == "" {
		return ErrInvalid
	}
	return database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var version, term int64
		if err := tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&version); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM cluster_nodes WHERE id=$1)", nodeID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		previous := None
		err := tx.QueryRow(ctx, "SELECT mode FROM node_faults WHERE node_id=$1", nodeID).Scan(&previous)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if previous == mode {
			return nil
		}
		if _, err := tx.Exec(ctx, "UPSERT INTO node_faults(node_id,mode,updated_at) VALUES($1,$2,clock_timestamp())", nodeID, string(mode)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT term FROM manager_lease WHERE singleton=true").Scan(&term); err != nil {
			return err
		}
		payload, _ := json.Marshal(struct {
			Mode      Mode `json:"mode"`
			Simulated bool `json:"simulated"`
		}{mode, true})
		_, err = tx.Exec(ctx, "INSERT INTO cluster_events(node_id,configuration_version,manager_term,kind,details) VALUES($1,$2,$3,'fault_changed',$4::JSONB)", nodeID, version, term, string(payload))
		return err
	})
}

// Current returns the effective mode. Disabled simulation always returns None,
// including when a development database still contains a previously selected mode.
func (s *Service) Current(ctx context.Context, nodeID string) (Mode, error) {
	if !s.Enabled {
		return None, nil
	}
	var mode Mode
	err := s.Pool.QueryRow(ctx, "SELECT mode FROM node_faults WHERE node_id=$1", nodeID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return None, nil
	}
	if err != nil {
		return None, err
	}
	return mode, nil
}
func (s *Service) Check(ctx context.Context, nodeID, component string) error {
	target := Mode(component)
	if target != Backend && target != SQL && target != Storage && target != Control {
		return ErrInvalid
	}
	mode, err := s.Current(ctx, nodeID)
	if err != nil {
		return err
	}
	if mode == Total || mode == target {
		return ErrInjected
	}
	return nil
}
