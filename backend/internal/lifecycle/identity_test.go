package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	madmin "github.com/minio/madmin-go/v3"
)

type replacementFake struct {
	fakeAdapter
	absent  bool
	removes int
}

func (f *replacementFake) OldStorageAbsent(context.Context, Request) (bool, error) {
	return f.absent, nil
}
func (f *replacementFake) RemoveOldStorage(context.Context, Request) error {
	f.removes++
	f.absent = true
	return nil
}
func TestStorageIdentityRecovery(t *testing.T) {
	ctx, pool := setup(t)
	store := cluster.New(pool)
	fake := &replacementFake{}
	svc := &Service{Pool: pool, Adapter: fake}
	var nodes []cluster.Node
	for _, name := range []string{"manager", "peer", "candidate"} {
		n, err := store.Register(ctx, reg(name))
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
		o, err := svc.ObserveIdentity(ctx, n.ID, request(name).Identity)
		if err != nil || o.Changed || o.Blocked || o.StorageGeneration != n.StorageGeneration {
			t.Fatalf("baseline %+v %v", o, err)
		}
	}
	lease, err := store.AcquireLease(ctx, nodes[0].ID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err = store.StartSync(ctx, lease, n.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Admit(ctx, lease, n.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	target := nodes[2]
	old := request("candidate").Identity
	newID := old
	newID.DeploymentID = "new-volume"
	obs, err := svc.ObserveIdentity(ctx, target.ID, newID)
	if err != nil || !obs.Changed || !obs.Blocked || obs.StorageGeneration == target.StorageGeneration {
		t.Fatalf("rotation %+v %v", obs, err)
	}
	repeat, err := svc.ObserveIdentity(ctx, target.ID, newID)
	if err != nil || repeat.Changed || repeat.StorageGeneration != obs.StorageGeneration {
		t.Fatalf("idempotency %+v %v", repeat, err)
	}
	if _, err = svc.ObserveIdentity(ctx, target.ID, old); !errors.Is(err, ErrConflict) {
		t.Fatalf("tombstone %v", err)
	}
	otherID := newID
	otherID.DeploymentID = "another-volume"
	if _, err = svc.ObserveIdentity(ctx, target.ID, otherID); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwritten intent %v", err)
	}
	snap, err := store.Snapshot(ctx)
	if err != nil || len(snap.Members) != 2 {
		t.Fatalf("fenced membership %+v %v", snap, err)
	}
	if err = store.StartSync(ctx, lease, target.ID); !errors.Is(err, cluster.ErrConflict) {
		t.Fatalf("premature sync %v", err)
	}
	op, err := svc.BeginPendingReplacement(ctx, lease)
	if err != nil || op.Kind != "replace" || op.Request.PreviousIdentity.DeploymentID != old.DeploymentID {
		t.Fatalf("replacement %+v %v", op, err)
	}
	if err = svc.Step(ctx, lease, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(ctx, lease, op.ID); err != nil {
		t.Fatal(err)
	}
	if fake.removes != 1 || fake.storageCalls != 1 {
		t.Fatalf("remote steps %+v", fake)
	}
	unchanged, err := svc.ObserveIdentity(ctx, target.ID, newID)
	if err != nil || unchanged.Changed || unchanged.Blocked {
		t.Fatalf("confirmed identity %+v %v", unchanged, err)
	}
	if _, err = svc.ObserveIdentity(ctx, target.ID, old); !errors.Is(err, ErrConflict) {
		t.Fatalf("old volume resurrected %v", err)
	}
	if err = store.StartSync(ctx, lease, target.ID); err != nil {
		t.Fatal(err)
	}
	// A published empty file still needs a receipt in the new volume generation.
	var user, upload string
	hash := sha256.Sum256(nil)
	if err = pool.QueryRow(ctx, `INSERT INTO users(login,password_hash) VALUES('identity-fixture','hash') RETURNING id::STRING`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO upload_operations(owner_id,name,idempotency_key,size_bytes,sha256,manifest,part_count,status,phase) VALUES($1,'empty',gen_random_uuid(),0,$2,'[]',0,'available','complete') RETURNING id::STRING`, user, hash[:]).Scan(&upload); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO files(operation_id,owner_id,name,configuration_version) VALUES($1,$2,'empty',0)`, upload, user); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE cluster_configuration SET publication_generation=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_copies(operation_id,node_id,storage_generation,object_key,s3_version_id,size_bytes,sha256) VALUES($1,$2,$3,'object','version',0,$4)`, upload, target.ID, target.StorageGeneration, hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Admit(ctx, lease, target.ID, 1); !errors.Is(err, cluster.ErrSyncRequired) {
		t.Fatalf("stale receipt admitted %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_copies(operation_id,node_id,storage_generation,object_key,s3_version_id,size_bytes,sha256) VALUES($1,$2,$3,'object','new-version',0,$4)`, upload, target.ID, obs.StorageGeneration, hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Admit(ctx, lease, target.ID, 1); err != nil {
		t.Fatal(err)
	}
	t.Log("generation rotated once; old deployment rejected; remove-old/add-new completed; stale receipt rejected and fresh generation admitted")
}
func TestReplacementMinIOPartialReconciliation(t *testing.T) {
	req := request("new")
	old := req.Identity
	old.DeploymentID = "lost-volume"
	req.PreviousIdentity = &old
	peer2 := req.Peers[0]
	peer2.DeploymentID = "peer2"
	peer2.SiteName = "peer2"
	peer2.StorageEndpoint = "http://peer2:9000"
	peer2.CredentialProfile = "peer2"
	req.Peers = append(req.Peers, peer2)
	binary := filepath.Join(t.TempDir(), "cockroach")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '[{\"cluster_id\":\"cluster\"}]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &Remote{Binary: binary, Profiles: map[string]Profile{}}
	for _, i := range append(append([]Identity{}, req.Peers...), req.Identity) {
		r.Profiles[i.CredentialProfile] = Profile{AccessKey: "access", SecretKey: "secretsecret", SQLGateway: "sql:26257", Insecure: true}
	}
	removed := map[string]bool{}
	calls := 0
	unavailable := false
	r.Transport = roundTrip(func(q *http.Request) (*http.Response, error) {
		var value any
		host := strings.Split(q.URL.Host, ":")[0]
		if unavailable && host == "peer2" {
			return nil, errors.New("offline survivor")
		}
		switch q.URL.Path {
		case "/minio/admin/v3/info":
			value = map[string]string{"deploymentID": host}
		case "/minio/admin/v3/site-replication/info":
			info := madmin.SiteReplicationInfo{}
			if host != "new" {
				info.Enabled = true
				list := req.Peers
				if !removed[host] {
					list = append(append([]Identity{}, list...), old)
				}
				for _, i := range list {
					info.Sites = append(info.Sites, madmin.PeerInfo{DeploymentID: i.DeploymentID, Name: i.SiteName, Endpoint: i.StorageEndpoint})
				}
			}
			value = info
		case "/minio/admin/v3/site-replication/remove":
			var body madmin.SRRemoveReq
			if err := json.NewDecoder(q.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.RemoveAll || len(body.SiteNames) != 1 || body.SiteNames[0] != "new" {
				t.Fatalf("unsafe remove %+v", body)
			}
			removed[host] = true
			calls++
			value = madmin.ReplicateRemoveStatus{Status: madmin.ReplicateRemoveStatusPartial, ErrDetail: "old volume unreachable"}
		case "/":
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets></Buckets></ListAllMyBucketsResult>`)), Request: q}, nil
		default:
			t.Fatalf("unexpected path %s", q.URL.Path)
		}
		b, _ := json.Marshal(value)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: q}, nil
	})
	ctx := context.Background()
	if done, err := r.OldStorageAbsent(ctx, req); err != nil || done {
		t.Fatalf("old present %v %v", done, err)
	}
	if err := r.RemoveOldStorage(ctx, req); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("survivors updated %d", calls)
	}
	if done, err := r.OldStorageAbsent(ctx, req); err != nil || !done {
		t.Fatalf("old absent %v %v", done, err)
	}
	unavailable = true
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if done, err := r.OldStorageAbsent(short, req); err == nil || done {
		t.Fatalf("missing survivor accepted %v %v", done, err)
	}
}
