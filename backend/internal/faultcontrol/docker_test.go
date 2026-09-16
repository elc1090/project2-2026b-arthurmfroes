package faultcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDockerDriverUsesFixedPauseAndUnpauseEndpoints(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	driver := NewDockerDriverWithClient(server.Client())
	target := ResolvedTarget{DockerContainer: "node-1-storage"}
	// Redirect the fixed Docker host through the fake transport.
	driver.client.Transport = rewriteTransport{base: server.URL, next: server.Client().Transport}
	if err := driver.Apply(context.Background(), OperationStop, target); err != nil {
		t.Fatal(err)
	}
	if err := driver.Apply(context.Background(), OperationRestore, target); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /containers/node-1-storage/pause", "POST /containers/node-1-storage/unpause"}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("path %d = %q, want %q", index, paths[index], want[index])
		}
	}
}

func TestDockerDriverDoesNotExposeDaemonBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "registry-token=secret", http.StatusInternalServerError)
	}))
	defer server.Close()
	driver := NewDockerDriverWithClient(&http.Client{Transport: rewriteTransport{base: server.URL, next: server.Client().Transport}})
	err := driver.Apply(context.Background(), OperationStop, ResolvedTarget{DockerContainer: "node-1"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
}

type rewriteTransport struct {
	base string
	next http.RoundTripper
}

func (r rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(r.base, "http://")
	return r.next.RoundTrip(clone)
}
