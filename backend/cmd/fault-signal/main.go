package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const procRoot = "/proc"

type process struct {
	pid   int
	pgrp  int
	state byte
}

type signalFunc func(int, syscall.Signal) error

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, procRoot, syscall.Kill))
}

func run(args []string, stdout, stderr io.Writer, root string, signal signalFunc) int {
	if len(args) != 1 || (args[0] != "stop" && args[0] != "restore" && args[0] != "status") {
		fmt.Fprintln(stderr, "usage: fault-signal stop|restore|status")
		return 2
	}
	workload, err := resolveWorkload(root)
	if err != nil {
		fmt.Fprintf(stderr, "invalid workload topology: %v\n", err)
		return 1
	}
	if err := signal(-workload.pgrp, 0); err != nil {
		fmt.Fprintln(stderr, "workload process group is unavailable")
		return 1
	}
	switch args[0] {
	case "status":
		fmt.Fprintln(stdout, stateName(workload.state))
		return 0
	case "stop":
		if err := signal(-workload.pgrp, syscall.SIGSTOP); err != nil {
			fmt.Fprintln(stderr, "could not stop workload process group")
			return 1
		}
		if err := waitForState(root, true); err != nil {
			fmt.Fprintln(stderr, "workload stop was not confirmed")
			return 1
		}
		fmt.Fprintln(stdout, "stopped")
	case "restore":
		if err := signal(-workload.pgrp, syscall.SIGCONT); err != nil {
			fmt.Fprintln(stderr, "could not restore workload process group")
			return 1
		}
		if err := waitForState(root, false); err != nil {
			fmt.Fprintln(stderr, "workload restore was not confirmed")
			return 1
		}
		fmt.Fprintln(stdout, "running")
	}
	return 0
}

func resolveWorkload(root string) (process, error) {
	children, err := os.ReadFile(filepath.Join(root, "1", "task", "1", "children"))
	if err != nil {
		return process{}, errors.New("cannot read init children")
	}
	fields := strings.Fields(string(children))
	if len(fields) != 1 {
		return process{}, errors.New("init must have exactly one direct child")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		return process{}, errors.New("invalid workload process")
	}
	stat, err := os.ReadFile(filepath.Join(root, fields[0], "stat"))
	if err != nil {
		return process{}, errors.New("cannot read workload state")
	}
	workload, err := parseStat(pid, string(stat))
	if err != nil {
		return process{}, err
	}
	if workload.pgrp != workload.pid {
		return process{}, errors.New("workload child is not its process-group leader")
	}
	return workload, nil
}

func parseStat(pid int, raw string) (process, error) {
	closing := strings.LastIndex(raw, ")")
	if closing < 0 {
		return process{}, errors.New("malformed workload state")
	}
	fields := strings.Fields(raw[closing+1:])
	if len(fields) < 3 || len(fields[0]) != 1 {
		return process{}, errors.New("malformed workload state")
	}
	parent, errParent := strconv.Atoi(fields[1])
	pgrp, errGroup := strconv.Atoi(fields[2])
	if errParent != nil || errGroup != nil || parent != 1 || pgrp <= 1 {
		return process{}, errors.New("workload is not a direct child in a separate process group")
	}
	return process{pid: pid, pgrp: pgrp, state: fields[0][0]}, nil
}

func waitForState(root string, stopped bool) error {
	deadline := time.Now().Add(time.Second)
	for {
		workload, err := resolveWorkload(root)
		if err != nil {
			return err
		}
		if isStopped(workload.state) == stopped {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("state transition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isStopped(state byte) bool { return state == 'T' || state == 't' }

func stateName(state byte) string {
	if isStopped(state) {
		return "stopped"
	}
	return "running"
}
