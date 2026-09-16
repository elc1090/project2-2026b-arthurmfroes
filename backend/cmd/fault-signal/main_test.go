package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func writeTopology(t *testing.T, root, children string, pid int, state byte, parent, group int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "1", "task", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "1", "task", "1", "children"), []byte(children), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Join(root, fmt.Sprint(pid)), 0o755); err != nil {
		t.Fatal(err)
	}
	stat := fmt.Sprintf("%d (workload with spaces) %c %d %d 0 0 0", pid, state, parent, group)
	if err := os.WriteFile(filepath.Join(root, fmt.Sprint(pid), "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveWorkloadRequiresOneDirectGroupLeader(t *testing.T) {
	for _, test := range []struct {
		name     string
		children string
		pid      int
		parent   int
		group    int
	}{
		{name: "no child"},
		{name: "multiple children", children: "21 22", pid: 21, parent: 1, group: 21},
		{name: "not direct", children: "21", pid: 21, parent: 8, group: 21},
		{name: "not group leader", children: "21", pid: 21, parent: 1, group: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTopology(t, root, test.children, test.pid, 'S', test.parent, test.group)
			if _, err := resolveWorkload(root); err == nil {
				t.Fatal("unexpected valid topology")
			}
		})
	}
	root := t.TempDir()
	writeTopology(t, root, "21\n", 21, 'S', 1, 21)
	workload, err := resolveWorkload(root)
	if err != nil || workload.pid != 21 || workload.pgrp != 21 || workload.state != 'S' {
		t.Fatalf("workload=%+v err=%v", workload, err)
	}
}

func TestRunUsesOnlyResolvedGroupAndConfirmsTransitions(t *testing.T) {
	root := t.TempDir()
	writeTopology(t, root, "31", 31, 'S', 1, 31)
	var signals []syscall.Signal
	signal := func(target int, value syscall.Signal) error {
		if target != -31 {
			t.Fatalf("target=%d", target)
		}
		signals = append(signals, value)
		if value == syscall.SIGSTOP {
			writeTopology(t, root, "31", 31, 'T', 1, 31)
		}
		if value == syscall.SIGCONT {
			writeTopology(t, root, "31", 31, 'S', 1, 31)
		}
		return nil
	}
	for _, test := range []struct {
		command, output string
	}{{"status", "running\n"}, {"stop", "stopped\n"}, {"status", "stopped\n"}, {"restore", "running\n"}} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{test.command}, &stdout, &stderr, root, signal); code != 0 || stdout.String() != test.output || stderr.Len() != 0 {
			t.Fatalf("command=%s code=%d stdout=%q stderr=%q", test.command, code, stdout.String(), stderr.String())
		}
	}
	want := []syscall.Signal{0, 0, syscall.SIGSTOP, 0, 0, syscall.SIGCONT}
	if fmt.Sprint(signals) != fmt.Sprint(want) {
		t.Fatalf("signals=%v want=%v", signals, want)
	}
}

func TestRunRejectsExternalSelector(t *testing.T) {
	for _, args := range [][]string{nil, {"kill"}, {"stop", "42"}, {"status", "minio"}} {
		var stdout, stderr bytes.Buffer
		called := false
		if code := run(args, &stdout, &stderr, t.TempDir(), func(int, syscall.Signal) error { called = true; return nil }); code != 2 || called || !strings.Contains(stderr.String(), "usage:") {
			t.Fatalf("args=%v code=%d called=%t stderr=%q", args, code, called, stderr.String())
		}
	}
}
