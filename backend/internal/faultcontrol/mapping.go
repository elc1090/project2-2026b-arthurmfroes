package faultcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type TargetConfig struct {
	NodeID          string    `json:"node_id"`
	Component       Component `json:"component"`
	DockerContainer string    `json:"docker_container,omitempty"`
	RailwayInstance string    `json:"railway_instance,omitempty"`
}

type Mapping struct {
	mode    Mode
	targets map[Target]ResolvedTarget
}

func NewMapping(mode Mode, configs []TargetConfig, actuatorContainer string) (*Mapping, error) {
	if mode != ModeDocker && mode != ModeRailwaySSH {
		return nil, fmt.Errorf("unsupported fault actuator mode %q", mode)
	}
	actuatorContainer = strings.TrimSpace(actuatorContainer)
	targets := make(map[Target]ResolvedTarget, len(configs))
	for _, config := range configs {
		target := Target{NodeID: strings.TrimSpace(config.NodeID), Component: config.Component}
		if err := target.Validate(); err != nil || target.Component == ComponentNode {
			return nil, fmt.Errorf("invalid configured target %q/%q", config.NodeID, config.Component)
		}
		resolved := ResolvedTarget{
			Target:          target,
			DockerContainer: strings.TrimSpace(config.DockerContainer),
			RailwayInstance: strings.TrimSpace(config.RailwayInstance),
		}
		switch mode {
		case ModeDocker:
			if resolved.DockerContainer == "" || resolved.RailwayInstance != "" {
				return nil, fmt.Errorf("target %s/%s has incompatible Docker configuration", target.NodeID, target.Component)
			}
			if resolved.DockerContainer == actuatorContainer {
				return nil, ErrActuatorTarget
			}
		case ModeRailwaySSH:
			if resolved.RailwayInstance == "" || resolved.DockerContainer != "" {
				return nil, fmt.Errorf("target %s/%s has incompatible Railway configuration", target.NodeID, target.Component)
			}
		}
		if _, duplicate := targets[target]; duplicate {
			return nil, fmt.Errorf("duplicate target %s/%s", target.NodeID, target.Component)
		}
		targets[target] = resolved
	}
	if len(targets) == 0 {
		return nil, errors.New("no fault targets are configured")
	}
	return &Mapping{mode: mode, targets: targets}, nil
}

func ParseTargetConfigs(raw string) ([]TargetConfig, error) {
	var configs []TargetConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configs); err != nil {
		return nil, fmt.Errorf("parse fault targets: %w", err)
	}
	return configs, nil
}

func (m *Mapping) Resolve(target Target) ([]ResolvedTarget, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if target.Component != ComponentNode {
		resolved, ok := m.targets[target]
		if !ok {
			return nil, ErrTargetNotFound
		}
		return []ResolvedTarget{resolved}, nil
	}
	components := []Component{ComponentBackend, ComponentSQL, ComponentStorage}
	resolved := make([]ResolvedTarget, 0, len(components))
	for _, component := range components {
		item, ok := m.targets[Target{NodeID: target.NodeID, Component: component}]
		if !ok {
			return nil, fmt.Errorf("%w: %s/%s", ErrTargetNotFound, target.NodeID, component)
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}
