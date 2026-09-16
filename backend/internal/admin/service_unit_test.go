package admin

import (
	"context"
	"reflect"
	"testing"
)

type stubFaultControl struct{ calls int }

func (s *stubFaultControl) Start(_ context.Context, input FaultRequest) (FaultAction, error) {
	s.calls++
	return FaultAction{ID: "action-1", NodeID: input.NodeID, Component: input.Component, Action: input.Action, Status: "requested", Results: []FaultResult{}}, nil
}
func (*stubFaultControl) Actions(context.Context) ([]FaultAction, error) { return []FaultAction{}, nil }

func TestSafeEventDetails(t *testing.T) {
	got := safeEventDetails([]byte(`{"reason":"storage probe failed","stage":"sync","secret":"do-not-return","nested":{"token":"hidden"}}`))
	want := map[string]string{"reason": "storage probe failed", "stage": "sync"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("details=%v", got)
	}
	if got := safeEventDetails([]byte(`not-json`)); len(got) != 0 {
		t.Fatalf("invalid details=%v", got)
	}
}

func TestFaultRequestValidation(t *testing.T) {
	valid := FaultRequest{NodeID: "backend-node-2", Component: FaultStorage, Action: FaultStop}
	if !validFaultRequest(valid) {
		t.Fatal("valid request rejected")
	}
	for _, invalid := range []FaultRequest{
		{},
		{NodeID: "backend-node-2", Component: "control", Action: FaultStop},
		{NodeID: "backend-node-2", Component: FaultNode, Action: "kill"},
	} {
		if validFaultRequest(invalid) {
			t.Fatalf("invalid request accepted: %+v", invalid)
		}
	}
}

func TestStartFaultDoesNotRequireClusterState(t *testing.T) {
	control := &stubFaultControl{}
	service := Service{FaultControl: control}
	action, err := service.StartFault(t.Context(), FaultRequest{NodeID: "backend-node-2", Component: FaultBackend, Action: FaultStop})
	if err != nil || action.ID != "action-1" || control.calls != 1 {
		t.Fatalf("action=%+v calls=%d err=%v", action, control.calls, err)
	}
	// Pool and Nodes are deliberately nil: accepting an actuator request cannot
	// mutate membership, manager leases, or cluster events before observation.
}
