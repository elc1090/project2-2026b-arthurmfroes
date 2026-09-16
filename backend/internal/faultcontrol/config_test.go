package faultcontrol

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresSecretAndClosedMode(t *testing.T) {
	base := map[string]string{
		"FAULT_ACTUATOR_MODE":    "docker",
		"FAULT_ACTUATOR_TOKEN":   "secret",
		"FAULT_ACTUATOR_TARGETS": targetJSON("node-1", "node-1-backend"),
	}
	for _, test := range []struct {
		name   string
		change func(map[string]string)
	}{
		{name: "missing secret", change: func(env map[string]string) { delete(env, "FAULT_ACTUATOR_TOKEN") }},
		{name: "unknown mode", change: func(env map[string]string) { env["FAULT_ACTUATOR_MODE"] = "auto" }},
		{name: "cross config", change: func(env map[string]string) { env["RAILWAY_SSH_KEY"] = "private" }},
		{name: "actuator target", change: func(env map[string]string) {
			env["FAULT_ACTUATOR_CONTAINER"] = "fault-actuator"
			env["FAULT_ACTUATOR_TARGETS"] = targetJSON("node-1", "fault-actuator")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := make(map[string]string, len(base))
			for key, value := range base {
				env[key] = value
			}
			test.change(env)
			if _, err := LoadConfig(func(key string) string { return env[key] }); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestLoadConfigRequiresCompleteRailwaySSHSecrets(t *testing.T) {
	base := map[string]string{
		"FAULT_ACTUATOR_MODE":         "railway-ssh",
		"FAULT_ACTUATOR_TOKEN":        "secret",
		"FAULT_ACTUATOR_TARGETS":      railwayTargetJSON("node-1", "instance-123"),
		"RAILWAY_SSH_HOST":            "ssh.railway.com",
		"RAILWAY_SSH_PRIVATE_KEY":     "private-key\nsecond-line",
		"RAILWAY_SSH_KNOWN_HOSTS":     "ssh.railway.com ssh-ed25519 AAAA",
		"RAILWAY_SSH_CONNECT_TIMEOUT": "4s",
		"RAILWAY_SSH_COMMAND_TIMEOUT": "12s",
	}
	config, err := LoadConfig(func(name string) string { return base[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.RailwayPrivateKey != base["RAILWAY_SSH_PRIVATE_KEY"] || config.RailwayKnownHosts != base["RAILWAY_SSH_KNOWN_HOSTS"] || config.RailwaySSH.Host != "ssh.railway.com" {
		t.Fatalf("Railway config = %#v", config.RailwaySSH)
	}

	for _, missing := range []string{"RAILWAY_SSH_HOST", "RAILWAY_SSH_PRIVATE_KEY", "RAILWAY_SSH_KNOWN_HOSTS"} {
		t.Run("missing "+missing, func(t *testing.T) {
			env := make(map[string]string, len(base))
			for name, value := range base {
				env[name] = value
			}
			delete(env, missing)
			if _, err := LoadConfig(func(name string) string { return env[name] }); err == nil {
				t.Fatal("expected incomplete Railway configuration error")
			}
		})
	}
}

func TestLoadConfigAcceptsDockerWithoutProductionFlags(t *testing.T) {
	env := map[string]string{
		"FAULT_ACTUATOR_MODE":    "docker",
		"FAULT_ACTUATOR_TOKEN":   "secret",
		"FAULT_ACTUATOR_TARGETS": targetJSON("node-1", "node-1-backend"),
	}
	config, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.DockerSocket != "/var/run/docker.sock" || config.Mode != ModeDocker {
		t.Fatalf("config = %#v", config)
	}
}

func targetJSON(node, container string) string {
	return `[{"node_id":"` + strings.ReplaceAll(node, `"`, ``) + `","component":"backend","docker_container":"` + strings.ReplaceAll(container, `"`, ``) + `"}]`
}

func railwayTargetJSON(node, instance string) string {
	return `[{"node_id":"` + strings.ReplaceAll(node, `"`, ``) + `","component":"backend","railway_instance":"` + strings.ReplaceAll(instance, `"`, ``) + `"}]`
}
