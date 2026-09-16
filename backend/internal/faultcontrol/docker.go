package faultcontrol

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DockerDriver struct {
	client *http.Client
	ready  func() error
}

func NewDockerDriver(socketPath string) (*DockerDriver, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, fmt.Errorf("Docker socket path is required")
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &DockerDriver{
		client: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		ready: func() error {
			connection, err := net.DialTimeout("unix", socketPath, time.Second)
			if err != nil {
				return safeError("Docker daemon is unavailable")
			}
			return connection.Close()
		},
	}, nil
}

func (d *DockerDriver) Ready() error {
	if d == nil || d.client == nil {
		return safeError("Docker driver is unavailable")
	}
	if d.ready != nil {
		return d.ready()
	}
	return nil
}

func NewDockerDriverWithClient(client *http.Client) *DockerDriver {
	return &DockerDriver{client: client}
}

func (d *DockerDriver) Apply(ctx context.Context, operation Operation, target ResolvedTarget) error {
	if d == nil || d.client == nil {
		return safeError("Docker driver is unavailable")
	}
	if target.DockerContainer == "" {
		return safeError("Docker target is unavailable")
	}
	action := "pause"
	if operation == OperationRestore {
		action = "unpause"
	} else if operation != OperationStop {
		return safeError("unsupported Docker operation")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/containers/"+url.PathEscape(target.DockerContainer)+"/"+action, nil)
	if err != nil {
		return safeError("prepare Docker request")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return safeError("Docker daemon is unavailable")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	// Docker reports already-paused and already-running containers as conflicts.
	// Treat those transitions as idempotent without exposing daemon response bodies.
	if response.StatusCode == http.StatusConflict {
		return nil
	}
	if response.StatusCode == http.StatusNotFound {
		return safeError("configured Docker target was not found")
	}
	return safeError(fmt.Sprintf("Docker action failed with status %d", response.StatusCode))
}
