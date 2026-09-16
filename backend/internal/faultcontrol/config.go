package faultcontrol

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Mode              Mode
	Port              int
	InternalToken     string
	DockerSocket      string
	ActuatorContainer string
	Targets           []TargetConfig
	RailwaySSH        RailwaySSHConfig
	RailwayPrivateKey string
	RailwayKnownHosts string
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
		if strings.TrimSpace(getenv("RAILWAY_SSH_KEY")) != "" || strings.TrimSpace(getenv("RAILWAY_SSH_PRIVATE_KEY")) != "" || strings.TrimSpace(getenv("RAILWAY_SSH_KNOWN_HOSTS")) != "" || strings.TrimSpace(getenv("RAILWAY_SSH_HOST")) != "" {
			return Config{}, fmt.Errorf("Railway SSH configuration is not allowed in docker mode")
		}
	case ModeRailwaySSH:
		if strings.TrimSpace(getenv("DOCKER_HOST")) != "" || config.ActuatorContainer != "" {
			return Config{}, fmt.Errorf("Docker configuration is not allowed in railway-ssh mode")
		}
		config.RailwaySSH = RailwaySSHConfig{
			Host:           strings.TrimSpace(getenv("RAILWAY_SSH_HOST")),
			ConnectTimeout: 5 * time.Second,
			CommandTimeout: 15 * time.Second,
		}
		config.RailwayPrivateKey = strings.TrimSpace(getenv("RAILWAY_SSH_PRIVATE_KEY"))
		config.RailwayKnownHosts = strings.TrimSpace(getenv("RAILWAY_SSH_KNOWN_HOSTS"))
		if config.RailwaySSH.Host == "" || config.RailwayPrivateKey == "" || config.RailwayKnownHosts == "" {
			return Config{}, fmt.Errorf("RAILWAY_SSH_HOST, RAILWAY_SSH_PRIVATE_KEY and RAILWAY_SSH_KNOWN_HOSTS are required")
		}
		if !validSSHHost(config.RailwaySSH.Host) {
			return Config{}, fmt.Errorf("RAILWAY_SSH_HOST is invalid")
		}
		if value := strings.TrimSpace(getenv("RAILWAY_SSH_CONNECT_TIMEOUT")); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil || duration < time.Second {
				return Config{}, fmt.Errorf("RAILWAY_SSH_CONNECT_TIMEOUT must be at least one second")
			}
			config.RailwaySSH.ConnectTimeout = duration
		}
		if value := strings.TrimSpace(getenv("RAILWAY_SSH_COMMAND_TIMEOUT")); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil || duration < time.Second {
				return Config{}, fmt.Errorf("RAILWAY_SSH_COMMAND_TIMEOUT must be at least one second")
			}
			config.RailwaySSH.CommandTimeout = duration
		}
	default:
		return Config{}, fmt.Errorf("FAULT_ACTUATOR_MODE must be docker or railway-ssh")
	}
	if _, err := NewMapping(config.Mode, config.Targets, config.ActuatorContainer); err != nil {
		return Config{}, err
	}
	return config, nil
}
