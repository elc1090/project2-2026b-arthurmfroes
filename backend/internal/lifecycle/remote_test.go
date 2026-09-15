package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	madmin "github.com/minio/madmin-go/v3"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRemoteMinIOContracts(t *testing.T) {
	req := request("new")
	all := append(append([]Identity{}, req.Peers...), req.Identity)
	profiles := map[string]Profile{}
	for _, i := range all {
		profiles[i.CredentialProfile] = Profile{AccessKey: "access", SecretKey: "secretsecret"}
	}
	changed := false
	removed := false
	calls := 0
	remote := &Remote{Profiles: profiles}
	remote.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("admin request not signed")
		}
		var payload any
		switch r.URL.Path {
		case "/minio/admin/v3/info":
			id := "peer"
			if r.URL.Host == "new:9000" {
				id = "new"
			}
			payload = map[string]string{"deploymentID": id}
		case "/minio/admin/v3/site-replication/info":
			list := req.Peers
			if changed {
				list = all
			}
			if removed {
				list = req.Peers
			}
			info := madmin.SiteReplicationInfo{Enabled: true}
			for _, i := range list {
				info.Sites = append(info.Sites, madmin.PeerInfo{DeploymentID: i.DeploymentID, Name: i.SiteName, Endpoint: i.StorageEndpoint})
			}
			if removed && r.URL.Host == "new:9000" {
				info = madmin.SiteReplicationInfo{Enabled: false}
			}
			payload = info
		case "/minio/admin/v3/site-replication/add":
			calls++
			if r.Method != "PUT" {
				t.Error("incorrect method")
			}
			plain, err := madmin.DecryptData("secretsecret", r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var sites []madmin.PeerSite
			if err = json.Unmarshal(plain, &sites); err != nil || len(sites) != 2 {
				t.Fatalf("encrypted peer payload %v %v", sites, err)
			}
			changed = true
			payload = madmin.ReplicateAddStatus{Success: true}
		case "/minio/admin/v3/site-replication/remove":
			var body madmin.SRRemoveReq
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if r.Method != "PUT" || body.RemoveAll || len(body.SiteNames) != 1 || body.SiteNames[0] != "new" {
				t.Fatalf("unsafe remove payload %+v", body)
			}
			removed = true
			payload = madmin.ReplicateRemoveStatus{Status: madmin.ReplicateRemoveStatusSuccess}
		default:
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		b, _ := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
	})
	ctx := context.Background()
	if err := remote.ChangeStorage(ctx, "join", req); err != nil {
		t.Fatal(err)
	}
	ok, err := remote.StorageMatches(ctx, "join", req)
	if err != nil || !ok || calls != 1 {
		t.Fatalf("join verification %v %v", ok, err)
	}
	if err := remote.ChangeStorage(ctx, "retire", req); err != nil {
		t.Fatal(err)
	}
	if ok, err := remote.StorageMatches(ctx, "retire", req); err != nil || !ok {
		t.Fatalf("retirement verification %v %v", ok, err)
	}
	mismatch := req
	mismatch.Identity.DeploymentID = "replacement-volume"
	if _, err := remote.StorageMatches(ctx, "join", mismatch); !errors.Is(err, ErrConflict) {
		t.Fatalf("storage identity overwrite %v", err)
	}
	bad := req
	bad.Identity.StorageEndpoint = "http://user:password@new:9000"
	if _, err := remote.client(bad.Identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("credential URL %v", err)
	}
}
func TestCockroachCommandContracts(t *testing.T) {
	// Executable fixture proves argument boundaries and JSON parsing, not database
	// decommission. No command here contacts the shared Cockroach deployment.
	dir := t.TempDir()
	binary := filepath.Join(dir, "cockroach")
	script := `#!/bin/sh
case "$1 $2" in
 "sql --execute=SELECT crdb_internal.cluster_id()::STRING AS cluster_id") printf '[{"cluster_id":"cluster"}]';;
 "node status") printf '[{"id":"4","membership":"decommissioned"}]';;
 "node decommission") printf 'ERROR: Cannot decommission nodes.\n' >&2; exit 1;;
 *) exit 2;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := &Remote{Binary: binary, Profiles: map[string]Profile{"new": {SQLGateway: "remote.example:26257", Insecure: true}}}
	i := request("new").Identity
	if done, err := r.SQLRemoved(context.Background(), i); err != nil || !done {
		t.Fatalf("parse %v %v", done, err)
	}
	if _, err := r.command(context.Background(), i, "node", "decommission", "4", "--dry-run", "--checks=strict"); !errors.Is(err, ErrTopology) {
		t.Fatalf("topology blocker %v", err)
	}
	i.ClusterID = "foreign"
	if _, err := r.SQLRemoved(context.Background(), i); !errors.Is(err, ErrConflict) {
		t.Fatalf("cluster mismatch %v", err)
	}
}
