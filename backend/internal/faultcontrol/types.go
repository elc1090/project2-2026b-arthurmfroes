package faultcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Mode string

const (
	ModeDocker     Mode = "docker"
	ModeRailwaySSH Mode = "railway-ssh"
)

type Component string

const (
	ComponentBackend Component = "backend"
	ComponentSQL     Component = "sql"
	ComponentStorage Component = "storage"
	ComponentNode    Component = "node"
)

type Operation string

const (
	OperationStop    Operation = "stop"
	OperationRestore Operation = "restore"
)

type State string

const (
	StateRequested State = "requested"
	StateRunning   State = "running"
	StateStopped   State = "stopped"
	StateRestored  State = "restored"
	StateFailed    State = "failed"
	StatePartial   State = "partial"
	StateUnknown   State = "unknown"
)

var (
	ErrInvalidRequest      = errors.New("invalid fault action request")
	ErrTargetNotFound      = errors.New("fault target is not configured")
	ErrActuatorTarget      = errors.New("the actuator cannot be a fault target")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for another action")
	ErrRailwayUnavailable  = errors.New("railway SSH driver is not configured")
	ErrDriverUnavailable   = errors.New("infrastructure driver is unavailable")
)

type Target struct {
	NodeID    string    `json:"node_id"`
	Component Component `json:"component"`
}

func (t Target) Validate() error {
	if strings.TrimSpace(t.NodeID) == "" {
		return fmt.Errorf("%w: node_id is required", ErrInvalidRequest)
	}
	switch t.Component {
	case ComponentBackend, ComponentSQL, ComponentStorage, ComponentNode:
		return nil
	default:
		return fmt.Errorf("%w: unknown component", ErrInvalidRequest)
	}
}

type Request struct {
	IdempotencyKey string    `json:"idempotency_key"`
	NodeID         string    `json:"node_id"`
	Component      Component `json:"component"`
	Operation      Operation `json:"action"`
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalidRequest)
	}
	if err := r.Target().Validate(); err != nil {
		return err
	}
	if r.Operation != OperationStop && r.Operation != OperationRestore {
		return fmt.Errorf("%w: unknown operation", ErrInvalidRequest)
	}
	return nil
}

func (r Request) Target() Target { return Target{NodeID: r.NodeID, Component: r.Component} }

type Result struct {
	Component Component `json:"component"`
	Status    State     `json:"status"`
	Error     string    `json:"error,omitempty"`
}

type Action struct {
	ID             string    `json:"id"`
	NodeID         string    `json:"node_id"`
	Component      Component `json:"component"`
	Operation      Operation `json:"action"`
	Status         State     `json:"status"`
	Results        []Result  `json:"results,omitempty"`
	Error          string    `json:"error,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	idempotencyKey string
}

type ResolvedTarget struct {
	Target          Target
	DockerContainer string
	RailwayInstance string
}

type Driver interface {
	Apply(ctx context.Context, operation Operation, target ResolvedTarget) error
}

type availability interface {
	Ready() error
}

type publicError struct{ message string }

func (e publicError) Error() string         { return e.message }
func (e publicError) PublicMessage() string { return e.message }

func safeError(message string) error { return publicError{message: message} }
