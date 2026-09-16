package faultcontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const railwayFaultHelper = "/usr/local/bin/fault-signal"

type RailwaySSHConfig struct {
	Host           string
	IdentityFile   string
	KnownHostsFile string
	ConnectTimeout time.Duration
	CommandTimeout time.Duration
}

type sshCommandRunner interface {
	Run(ctx context.Context, executable string, arguments ...string) ([]byte, error)
}

type execSSHRunner struct{}

func (execSSHRunner) Run(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, arguments...).CombinedOutput()
}

type RailwaySSHDriver struct {
	config     RailwaySSHConfig
	executable string
	runner     sshCommandRunner
}

type RailwaySSHSecretFiles struct {
	IdentityFile   string
	KnownHostsFile string
	directory      string
}

func MaterializeRailwaySSHSecrets(privateKey, knownHosts string) (RailwaySSHSecretFiles, func() error, error) {
	if strings.TrimSpace(privateKey) == "" || strings.TrimSpace(knownHosts) == "" {
		return RailwaySSHSecretFiles{}, nil, fmt.Errorf("Railway SSH secrets are required")
	}
	directory, err := os.MkdirTemp("", "fault-actuator-ssh-")
	if err != nil {
		return RailwaySSHSecretFiles{}, nil, fmt.Errorf("create Railway SSH secret directory")
	}
	cleanup := func() error { return os.RemoveAll(directory) }
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = cleanup()
		return RailwaySSHSecretFiles{}, nil, fmt.Errorf("secure Railway SSH secret directory")
	}
	files := RailwaySSHSecretFiles{
		IdentityFile: filepath.Join(directory, "id_ed25519"), KnownHostsFile: filepath.Join(directory, "known_hosts"), directory: directory,
	}
	for path, content := range map[string]string{files.IdentityFile: privateKey, files.KnownHostsFile: knownHosts} {
		if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
			_ = cleanup()
			return RailwaySSHSecretFiles{}, nil, fmt.Errorf("materialize Railway SSH secret")
		}
	}
	return files, cleanup, nil
}

func NewRailwaySSHDriver(config RailwaySSHConfig) (*RailwaySSHDriver, error) {
	executable, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("OpenSSH client is unavailable")
	}
	return newRailwaySSHDriver(config, executable, execSSHRunner{})
}

func newRailwaySSHDriver(config RailwaySSHConfig, executable string, runner sshCommandRunner) (*RailwaySSHDriver, error) {
	if strings.TrimSpace(executable) == "" || runner == nil {
		return nil, fmt.Errorf("OpenSSH client is unavailable")
	}
	if !validSSHHost(config.Host) || config.IdentityFile == "" || config.KnownHostsFile == "" {
		return nil, fmt.Errorf("Railway SSH configuration is incomplete")
	}
	if config.ConnectTimeout < time.Second || config.CommandTimeout < time.Second {
		return nil, fmt.Errorf("Railway SSH timeouts must be at least one second")
	}
	return &RailwaySSHDriver{config: config, executable: executable, runner: runner}, nil
}

func (d *RailwaySSHDriver) Ready() error {
	if d == nil || d.runner == nil {
		return safeError("Railway SSH driver is unavailable")
	}
	return nil
}

func (d *RailwaySSHDriver) Apply(ctx context.Context, operation Operation, target ResolvedTarget) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if !validSSHIdentifier(target.RailwayInstance) {
		return safeError("configured Railway target is invalid")
	}
	command := ""
	wantedStatus := ""
	switch operation {
	case OperationStop:
		command, wantedStatus = "stop", "stopped"
	case OperationRestore:
		command, wantedStatus = "restore", "running"
	default:
		return safeError("unsupported Railway operation")
	}
	output, err := d.run(ctx, target.RailwayInstance, command)
	if err == nil {
		return nil
	}
	if !ambiguousSSHFailure(ctx, output, err) {
		return classifySSHError(output, err)
	}

	statusOutput, statusErr := d.run(ctx, target.RailwayInstance, "status")
	if statusErr != nil {
		return fmt.Errorf("%w: confirmation failed", ErrIndeterminate)
	}
	status := strings.TrimSpace(string(statusOutput))
	if status == wantedStatus {
		return nil
	}
	if status == "running" || status == "stopped" {
		return safeError("Railway action was not applied")
	}
	return fmt.Errorf("%w: remote status is invalid", ErrIndeterminate)
}

func (d *RailwaySSHDriver) run(parent context.Context, instanceID, command string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, d.config.CommandTimeout)
	defer cancel()
	output, err := d.runner.Run(ctx, d.executable, d.arguments(instanceID, command)...)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return output, context.DeadlineExceeded
	}
	return output, err
}

func (d *RailwaySSHDriver) arguments(instanceID, command string) []string {
	connectSeconds := int(d.config.ConnectTimeout.Round(time.Second) / time.Second)
	return []string{
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + d.config.KnownHostsFile,
		"-o", "ConnectTimeout=" + strconv.Itoa(connectSeconds),
		"-i", d.config.IdentityFile,
		"--", instanceID + "@" + d.config.Host,
		railwayFaultHelper, command,
	}
}

func validSSHHost(host string) bool {
	if host == "" || strings.HasPrefix(host, "-") {
		return false
	}
	for _, character := range host {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func ambiguousSSHFailure(ctx context.Context, output []byte, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(string(output))
	for _, fragment := range []string{"host key verification failed", "remote host identification has changed", "permission denied", "no such identity", "could not resolve hostname", "connection refused"} {
		if strings.Contains(message, fragment) {
			return false
		}
	}
	for _, fragment := range []string{"connection reset", "connection closed", "broken pipe", "connection timed out", "operation timed out"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	var exitError *exec.ExitError
	return errors.As(err, &exitError) && exitError.ExitCode() == 255
}

func classifySSHError(output []byte, err error) error {
	message := strings.ToLower(string(output))
	if strings.Contains(message, "host key verification failed") || strings.Contains(message, "remote host identification has changed") {
		return safeError("Railway SSH host key verification failed")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: Railway SSH command timed out", ErrIndeterminate)
	}
	return safeError("Railway SSH command failed")
}
