package faultcontrol

import (
	"errors"
	"reflect"
	"testing"
)

func TestMappingResolvesExactTargetsAndWholeNode(t *testing.T) {
	configs := testTargets("node-1")
	mapping, err := NewMapping(ModeDocker, configs, "fault-actuator")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := mapping.Resolve(Target{NodeID: "node-1", Component: ComponentNode})
	if err != nil {
		t.Fatal(err)
	}
	components := []Component{resolved[0].Target.Component, resolved[1].Target.Component, resolved[2].Target.Component}
	if want := []Component{ComponentBackend, ComponentSQL, ComponentStorage}; !reflect.DeepEqual(components, want) {
		t.Fatalf("components = %v, want %v", components, want)
	}
	if _, err := mapping.Resolve(Target{NodeID: "node", Component: ComponentBackend}); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("partial node name must not resolve, got %v", err)
	}
}

func TestMappingRejectsInvalidAndActuatorTargets(t *testing.T) {
	tests := []struct {
		name    string
		mode    Mode
		targets []TargetConfig
		want    error
	}{
		{name: "unknown mode", mode: "auto", targets: testTargets("node-1")},
		{name: "actuator itself", mode: ModeDocker, targets: []TargetConfig{{NodeID: "node-1", Component: ComponentBackend, DockerContainer: "fault-actuator"}}, want: ErrActuatorTarget},
		{name: "cross mode", mode: ModeDocker, targets: []TargetConfig{{NodeID: "node-1", Component: ComponentBackend, RailwayInstance: "instance"}}},
		{name: "node pseudo target", mode: ModeDocker, targets: []TargetConfig{{NodeID: "node-1", Component: ComponentNode, DockerContainer: "node"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMapping(tt.mode, tt.targets, "fault-actuator")
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func testTargets(node string) []TargetConfig {
	return []TargetConfig{
		{NodeID: node, Component: ComponentBackend, DockerContainer: node + "-backend"},
		{NodeID: node, Component: ComponentSQL, DockerContainer: node + "-sql"},
		{NodeID: node, Component: ComponentStorage, DockerContainer: node + "-storage"},
	}
}
