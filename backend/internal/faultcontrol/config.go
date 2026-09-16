package faultcontrol

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Mode              Mode
	Port              int
	InternalToken     string
	DockerSocket      string
	ActuatorContainer string
	Targets           []TargetConfig
}

func LoadConfig(getenv func(string) string) (Config, error) {
	config := Config{
		Mode:              Mode(strings.TrimSpace(getenv("FAULT_ACTUATOR_MODE"))),
		InternalToken:     strings.TrimSpace(getenv("FAULT_ACTUATOR_TOKEN")),
		ActuatorContainer: strings.TrimSpace(getenv("FAULT_ACTUATOR_CONTAINER")),
		Port:              8090,
	}
	if config.InternalToken == "" {
		return Config{}, fmt.Errorf("FAULT_ACTUATOR_TOKEN is required")
	}
	if rawPort := strings.TrimSpace(getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("PORT must be between 1 and 65535")
		}
		config.Port = port
	}
	targets, err := ParseTargetConfigs(getenv("FAULT_ACTUATOR_TARGETS"))
	if err != nil {
		return Config{}, err
	}
	config.Targets = targets
	switch config.Mode {
	case ModeDocker:
		dockerHost := strings.TrimSpace(getenv("DOCKER_HOST"))
		if dockerHost == "" {
			dockerHost = "unix:///var/run/docker.sock"
		}
		parsed, err := url.Parse(dockerHost)
		if err != nil || parsed.Scheme != "unix" || parsed.Path == "" {
			return Config{}, fmt.Errorf("DOCKER_HOST must be a unix socket URL")
		}
		config.DockerSocket = parsed.Path
		if strings.TrimSpace(getenv("RAILWAY_SSH_KEY")) != "" {
			return Config{}, fmt.Errorf("Railway SSH configuration is not allowed in docker mode")
		}
	case ModeRailwaySSH:
		if strings.TrimSpace(getenv("DOCKER_HOST")) != "" {
			return Config{}, fmt.Errorf("Docker configuration is not allowed in railway-ssh mode")
		}
	default:
		return Config{}, fmt.Errorf("FAULT_ACTUATOR_MODE must be docker or railway-ssh")
	}
	if _, err := NewMapping(config.Mode, config.Targets, config.ActuatorContainer); err != nil {
		return Config{}, err
	}
	return config, nil
}
