package faultcontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Service struct {
	driver  Driver
	mapping *Mapping

	mu      sync.RWMutex
	actions map[string]Action
	keys    map[string]string
}

func NewService(driver Driver, mapping *Mapping) (*Service, error) {
	if driver == nil || mapping == nil {
		return nil, fmt.Errorf("fault control driver and mapping are required")
	}
	return &Service{driver: driver, mapping: mapping, actions: make(map[string]Action), keys: make(map[string]string)}, nil
}

func (s *Service) Submit(ctx context.Context, request Request) (Action, bool, error) {
	if err := request.Validate(); err != nil {
		return Action{}, false, err
	}
	if checker, ok := s.driver.(availability); ok {
		if err := checker.Ready(); err != nil {
			return Action{}, false, fmt.Errorf("%w: %v", ErrDriverUnavailable, err)
		}
	}
	target := request.Target()
	resolved, err := s.mapping.Resolve(target)
	if err != nil {
		return Action{}, false, err
	}

	s.mu.Lock()
	if id, ok := s.keys[request.IdempotencyKey]; ok {
		existing := s.actions[id]
		s.mu.Unlock()
		if existing.NodeID != request.NodeID || existing.Component != request.Component || existing.Operation != request.Operation {
			return Action{}, false, ErrIdempotencyConflict
		}
		return cloneAction(existing), false, nil
	}
	id, err := actionID()
	if err != nil {
		s.mu.Unlock()
		return Action{}, false, fmt.Errorf("create action identifier")
	}
	action := Action{ID: id, NodeID: request.NodeID, Component: request.Component, Operation: request.Operation, Status: StateRequested, UpdatedAt: time.Now().UTC(), idempotencyKey: request.IdempotencyKey}
	s.actions[id] = action
	s.keys[request.IdempotencyKey] = id
	s.mu.Unlock()

	if request.Operation == OperationRestore && len(resolved) > 1 {
		for left, right := 0, len(resolved)-1; left < right; left, right = left+1, right-1 {
			resolved[left], resolved[right] = resolved[right], resolved[left]
		}
	}
	go s.execute(context.WithoutCancel(ctx), id, request.Operation, resolved)
	return cloneAction(action), true, nil
}

func (s *Service) Get(id string) (Action, bool) {
	s.mu.RLock()
	action, ok := s.actions[id]
	s.mu.RUnlock()
	return cloneAction(action), ok
}

func (s *Service) List() []Action {
	s.mu.RLock()
	actions := make([]Action, 0, len(s.actions))
	for _, action := range s.actions {
		actions = append(actions, cloneAction(action))
	}
	s.mu.RUnlock()
	sort.Slice(actions, func(left, right int) bool {
		if actions[left].UpdatedAt.Equal(actions[right].UpdatedAt) {
			return actions[left].ID > actions[right].ID
		}
		return actions[left].UpdatedAt.After(actions[right].UpdatedAt)
	})
	return actions
}

func (s *Service) execute(ctx context.Context, id string, operation Operation, targets []ResolvedTarget) {
	now := time.Now().UTC()
	s.update(id, func(action *Action) { action.Status, action.UpdatedAt = StateRunning, now })
	results := make([]Result, 0, len(targets))
	failures := 0
	for _, target := range targets {
		result := Result{Component: target.Target.Component, Status: completedState(operation)}
		if err := s.driver.Apply(ctx, operation, target); err != nil {
			failures++
			result.Status = StateFailed
			result.Error = sanitizeError(err)
		}
		results = append(results, result)
	}
	completed := time.Now().UTC()
	s.update(id, func(action *Action) {
		action.Results = results
		action.UpdatedAt = completed
		switch {
		case failures == 0:
			action.Status = completedState(operation)
		case failures == len(results):
			action.Status = StateFailed
			action.Error = "infrastructure action failed"
		default:
			action.Status = StatePartial
			action.Error = "infrastructure action completed partially"
		}
	})
}

func completedState(operation Operation) State {
	if operation == OperationRestore {
		return StateRestored
	}
	return StateStopped
}

func (s *Service) update(id string, apply func(*Action)) {
	s.mu.Lock()
	action := s.actions[id]
	apply(&action)
	s.actions[id] = action
	s.mu.Unlock()
}

func actionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "infrastructure action timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "infrastructure action was canceled"
	}
	var public interface{ PublicMessage() string }
	if errors.As(err, &public) {
		return public.PublicMessage()
	}
	return "infrastructure action failed"
}

func cloneAction(action Action) Action {
	action.Results = append([]Result(nil), action.Results...)
	return action
}
