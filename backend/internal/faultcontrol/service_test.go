package faultcontrol

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingDriver struct {
	mu      sync.Mutex
	calls   []ResolvedTarget
	failFor Component
	block   chan struct{}
	error   error
}

func (d *recordingDriver) Apply(ctx context.Context, _ Operation, target ResolvedTarget) error {
	if d.block != nil {
		select {
		case <-d.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	d.calls = append(d.calls, target)
	d.mu.Unlock()
	if target.Target.Component == d.failFor {
		return d.error
	}
	return nil
}

func TestServiceRunsAsynchronouslyAndIsIdempotent(t *testing.T) {
	driver := &recordingDriver{block: make(chan struct{})}
	service := newTestService(t, driver)
	request := Request{IdempotencyKey: "request-1", NodeID: "node-1", Component: ComponentBackend, Operation: OperationStop}
	first, created, err := service.Submit(context.Background(), request)
	if err != nil || !created {
		t.Fatalf("Submit() = %#v, %v, %v", first, created, err)
	}
	if first.Status != StateQueued {
		t.Fatalf("status = %q, want queued", first.Status)
	}
	second, created, err := service.Submit(context.Background(), request)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("idempotent Submit() = %#v, %v, %v", second, created, err)
	}
	conflict := request
	conflict.Operation = OperationRestore
	if _, _, err := service.Submit(context.Background(), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting Submit error = %v", err)
	}
	close(driver.block)
	action := awaitTerminal(t, service, first.ID)
	if action.Status != StateSucceeded {
		t.Fatalf("status = %q, want succeeded", action.Status)
	}
}

func TestServiceReportsPartialNodeActionAndSanitizesDriverError(t *testing.T) {
	driver := &recordingDriver{failFor: ComponentDatabase, error: errors.New("token=provider-secret")}
	service := newTestService(t, driver)
	action, _, err := service.Submit(context.Background(), Request{IdempotencyKey: "partial", NodeID: "node-1", Component: ComponentNode, Operation: OperationStop})
	if err != nil {
		t.Fatal(err)
	}
	action = awaitTerminal(t, service, action.ID)
	if action.Status != StatePartial || len(action.Results) != 3 {
		t.Fatalf("action = %#v", action)
	}
	encoded := action.Error
	for _, result := range action.Results {
		encoded += result.Error
	}
	if strings.Contains(encoded, "provider-secret") {
		t.Fatalf("provider secret leaked: %q", encoded)
	}
	if action.Results[0].Component != ComponentBackend {
		t.Fatalf("stop order starts with %q", action.Results[0].Component)
	}
}

func TestWholeNodeRestoreUsesReverseOrder(t *testing.T) {
	driver := &recordingDriver{}
	service := newTestService(t, driver)
	action, _, err := service.Submit(context.Background(), Request{IdempotencyKey: "restore", NodeID: "node-1", Component: ComponentNode, Operation: OperationRestore})
	if err != nil {
		t.Fatal(err)
	}
	action = awaitTerminal(t, service, action.ID)
	want := []Component{ComponentStorage, ComponentDatabase, ComponentBackend}
	for index, component := range want {
		if action.Results[index].Component != component {
			t.Fatalf("result %d = %q, want %q", index, action.Results[index].Component, component)
		}
	}
}

func newTestService(t *testing.T, driver Driver) *Service {
	t.Helper()
	mapping, err := NewMapping(ModeDocker, testTargets("node-1"), "fault-actuator")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(driver, mapping)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func awaitTerminal(t *testing.T, service *Service, id string) Action {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		action, ok := service.Get(id)
		if ok && action.Status != StateQueued && action.Status != StateRunning {
			return action
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("action did not complete")
	return Action{}
}
