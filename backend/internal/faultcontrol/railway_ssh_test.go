package faultcontrol

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type sshResponse struct {
	output string
	err    error
}

type fakeSSHRunner struct {
	mu        sync.Mutex
	responses []sshResponse
	calls     [][]string
}

func (r *fakeSSHRunner) Run(_ context.Context, executable string, arguments ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{executable}, arguments...))
	if len(r.responses) == 0 {
		return nil, nil
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return []byte(response.output), response.err
}

func TestRailwaySSHUsesFixedStrictArguments(t *testing.T) {
	runner := &fakeSSHRunner{}
	driver := testRailwayDriver(t, runner)
	target := ResolvedTarget{Target: Target{NodeID: "node;not-a-command", Component: ComponentBackend}, RailwayInstance: "instance-123"}
	if err := driver.Apply(context.Background(), OperationStop, target); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/test/ssh", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=/secrets/known_hosts", "-o", "ConnectTimeout=3", "-i", "/secrets/id_ed25519",
		"--", "instance-123@ssh.railway.com", railwayFaultHelper, "stop",
	}
	if !reflect.DeepEqual(runner.calls, [][]string{want}) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, [][]string{want})
	}
	if strings.Contains(strings.Join(runner.calls[0], " "), target.Target.NodeID) {
		t.Fatal("request node ID was interpolated into SSH arguments")
	}
}

func TestRailwaySSHConfirmsInterruptedAndTimedOutActions(t *testing.T) {
	for _, test := range []struct {
		name  string
		first error
	}{
		{name: "connection interrupted", first: errors.New("connection reset by peer")},
		{name: "command timed out", first: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeSSHRunner{responses: []sshResponse{{output: test.first.Error(), err: test.first}, {output: "stopped\n"}}}
			driver := testRailwayDriver(t, runner)
			if err := driver.Apply(context.Background(), OperationStop, ResolvedTarget{RailwayInstance: "instance-123"}); err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 2 || runner.calls[0][len(runner.calls[0])-1] != "stop" || runner.calls[1][len(runner.calls[1])-1] != "status" {
				t.Fatalf("calls = %#v", runner.calls)
			}
		})
	}
}

func TestRailwaySSHReturnsUnknownWhenConfirmationFails(t *testing.T) {
	runner := &fakeSSHRunner{responses: []sshResponse{
		{output: "connection closed", err: errors.New("exit status 255")},
		{output: "connection timed out", err: errors.New("exit status 255")},
	}}
	driver := testRailwayDriver(t, runner)
	err := driver.Apply(context.Background(), OperationRestore, ResolvedTarget{RailwayInstance: "instance-123"})
	if !errors.Is(err, ErrIndeterminate) {
		t.Fatalf("error = %v, want indeterminate", err)
	}
}

func TestRailwaySSHHostKeyFailureIsClosedAndNotRetried(t *testing.T) {
	runner := &fakeSSHRunner{responses: []sshResponse{{output: "Host key verification failed.", err: errors.New("exit status 255")}}}
	driver := testRailwayDriver(t, runner)
	err := driver.Apply(context.Background(), OperationStop, ResolvedTarget{RailwayInstance: "instance-123"})
	if err == nil || !strings.Contains(err.Error(), "host key verification") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("host key failure made %d calls", len(runner.calls))
	}
}

func TestRailwaySSHRejectsUnsafeTargetBeforeExecution(t *testing.T) {
	runner := &fakeSSHRunner{}
	driver := testRailwayDriver(t, runner)
	if err := driver.Apply(context.Background(), OperationStop, ResolvedTarget{RailwayInstance: "-oProxyCommand=bad"}); err == nil {
		t.Fatal("expected unsafe target error")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unsafe target executed %#v", runner.calls)
	}
}

func TestRailwaySSHDoesNotRetryDefiniteHelperFailure(t *testing.T) {
	runner := &fakeSSHRunner{responses: []sshResponse{{output: "unexpected process topology", err: errors.New("exit status 1")}}}
	driver := testRailwayDriver(t, runner)
	err := driver.Apply(context.Background(), OperationStop, ResolvedTarget{RailwayInstance: "instance-123"})
	if err == nil || strings.Contains(err.Error(), "topology") {
		t.Fatalf("error must be sanitized, got %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("definite helper failure made %d calls", len(runner.calls))
	}
}

func TestMaterializeRailwaySSHSecretsUsesPrivatePermissionsAndCleansUp(t *testing.T) {
	files, cleanup, err := MaterializeRailwaySSHSecrets("PRIVATE\nKEY", "ssh.railway.com ssh-ed25519 AAAA")
	if err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(files.directory)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := directoryInfo.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("directory permissions = %o, want 700", permissions)
	}
	for _, path := range []string{files.IdentityFile, files.KnownHostsFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", path, permissions)
		}
	}
	key, err := os.ReadFile(files.IdentityFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != "PRIVATE\nKEY\n" {
		t.Fatalf("materialized key changed: %q", key)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(files.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret directory still exists: %v", err)
	}
}

func TestMaterializeRailwaySSHSecretsRejectsMissingContent(t *testing.T) {
	if _, _, err := MaterializeRailwaySSHSecrets("", "known-host"); err == nil {
		t.Fatal("expected missing private key error")
	}
}

func testRailwayDriver(t *testing.T, runner sshCommandRunner) *RailwaySSHDriver {
	t.Helper()
	driver, err := newRailwaySSHDriver(RailwaySSHConfig{
		Host: "ssh.railway.com", IdentityFile: "/secrets/id_ed25519", KnownHostsFile: "/secrets/known_hosts",
		ConnectTimeout: 3 * time.Second, CommandTimeout: 5 * time.Second,
	}, "/test/ssh", runner)
	if err != nil {
		t.Fatal(err)
	}
	return driver
}
