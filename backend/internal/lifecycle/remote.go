package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	madmin "github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Profile is trusted server configuration, never a request payload. SQLGateway
// must be a surviving Cockroach gateway, not the node being retired.
type Profile struct {
	SQLGateway, CertsDir string
	Insecure             bool
	AccessKey, SecretKey string
}
type Remote struct {
	Binary    string
	Profiles  map[string]Profile
	Timeout   time.Duration
	Transport http.RoundTripper
}

func (r *Remote) profile(i Identity) (Profile, error) {
	p, ok := r.Profiles[i.CredentialProfile]
	if !ok {
		return p, ErrBlocked
	}
	return p, nil
}
func (r *Remote) command(ctx context.Context, i Identity, args ...string) ([]map[string]string, error) {
	p, err := r.profile(i)
	if err != nil {
		return nil, err
	}
	if _, _, err := net.SplitHostPort(p.SQLGateway); err != nil {
		return nil, ErrBlocked
	}
	binary := r.Binary
	if binary == "" {
		binary = "cockroach"
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args = append(args, "--host="+p.SQLGateway, "--format=json")
	if p.Insecure {
		args = append(args, "--insecure")
	} else {
		if p.CertsDir == "" {
			return nil, ErrBlocked
		}
		args = append(args, "--certs-dir="+p.CertsDir)
	}
	// Arguments never contain access keys or passwords. CLI output is private:
	// failures may include connection metadata, so return only a fixed error.
	b, err := exec.CommandContext(ctx, binary, args...).Output()
	if err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) && bytes.Contains(exited.Stderr, []byte("Cannot decommission nodes")) {
			return nil, ErrTopology
		}
		return nil, ErrBlocked
	}
	var rows []map[string]string
	if err = json.Unmarshal(b, &rows); err != nil {
		return nil, ErrBlocked
	}
	return rows, nil
}
func (r *Remote) verifySQL(ctx context.Context, i Identity) error {
	rows, err := r.command(ctx, i, "sql", "--execute=SELECT crdb_internal.cluster_id()::STRING AS cluster_id")
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0]["cluster_id"] != i.ClusterID {
		return ErrConflict
	}
	return nil
}
func (r *Remote) SQLRemoved(ctx context.Context, i Identity) (bool, error) {
	if err := r.verifySQL(ctx, i); err != nil {
		return false, err
	}
	rows, err := r.command(ctx, i, "node", "status", "--all", "--decommission")
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row["id"] == strconv.FormatInt(i.SQLNodeID, 10) {
			return row["membership"] == "decommissioned", nil
		}
	}
	return false, ErrConflict
}
func (r *Remote) RemoveSQL(ctx context.Context, i Identity) error {
	done, err := r.SQLRemoved(ctx, i)
	if err != nil || done {
		return err
	}
	// Each retry repeats the strict readiness check; no topology bypass flags.
	_, err = r.command(ctx, i, "node", "decommission", strconv.FormatInt(i.SQLNodeID, 10), "--checks=strict", "--wait=all")
	return err
}
func (r *Remote) client(i Identity) (*madmin.AdminClient, error) {
	p, err := r.profile(i)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(i.StorageEndpoint)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrConflict
	}
	c, err := madmin.New(u.Host, p.AccessKey, p.SecretKey, u.Scheme == "https")
	if err != nil {
		return nil, ErrBlocked
	}
	if r.Transport != nil {
		c.SetCustomTransport(r.Transport)
	}
	return c, nil
}
func (r *Remote) site(ctx context.Context, i Identity) (*madmin.AdminClient, madmin.SiteReplicationInfo, error) {
	ctx, cancel := r.bounded(ctx)
	defer cancel()
	c, err := r.client(i)
	if err != nil {
		return nil, madmin.SiteReplicationInfo{}, err
	}
	info, err := c.ServerInfo(ctx)
	if err != nil {
		return nil, madmin.SiteReplicationInfo{}, ErrBlocked
	}
	if info.DeploymentID != i.DeploymentID {
		return nil, madmin.SiteReplicationInfo{}, ErrConflict
	}
	sites, err := c.SiteReplicationInfo(ctx)
	if err != nil {
		return nil, sites, ErrBlocked
	}
	return c, sites, nil
}
func participants(req Request) ([]Identity, error) {
	seen := map[string]bool{}
	names := map[string]bool{}
	all := append(append([]Identity{}, req.Peers...), req.Identity)
	if len(req.Peers) == 0 {
		return nil, ErrBlocked
	}
	for _, i := range all {
		if i.DeploymentID == "" || i.SiteName == "" || seen[i.DeploymentID] || names[i.SiteName] || i.ClusterID != req.Identity.ClusterID {
			return nil, ErrConflict
		}
		seen[i.DeploymentID] = true
		names[i.SiteName] = true
	}
	return all, nil
}
func exact(info madmin.SiteReplicationInfo, want []Identity) bool {
	if !info.Enabled || len(info.Sites) != len(want) {
		return false
	}
	for _, i := range want {
		found := false
		for _, p := range info.Sites {
			if p.DeploymentID == i.DeploymentID && p.Name == i.SiteName && strings.TrimRight(p.Endpoint, "/") == strings.TrimRight(i.StorageEndpoint, "/") {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (r *Remote) StorageMatches(ctx context.Context, kind string, req Request) (bool, error) {
	all, err := participants(req)
	if err != nil {
		return false, err
	}
	want := all
	if kind == "retire" {
		want = req.Peers
	}
	matched := true
	for _, i := range all {
		_, info, err := r.site(ctx, i)
		if err != nil {
			return false, err
		}
		if kind == "retire" && i.DeploymentID == req.Identity.DeploymentID {
			if info.Enabled {
				matched = false
			}
		} else if !exact(info, want) {
			matched = false
		}
	}
	return matched, nil
}
func (r *Remote) Preflight(ctx context.Context, kind string, req Request) error {
	ctx, cancel := r.bounded(ctx)
	defer cancel()
	all, err := participants(req)
	if err != nil {
		return err
	}
	if err = r.verifySQL(ctx, req.Identity); err != nil {
		return err
	}
	removed, err := r.SQLRemoved(ctx, req.Identity)
	if err != nil {
		return err
	}
	if removed {
		return ErrConflict
	}
	if kind == "retire" {
		if _, err = r.command(ctx, req.Identity, "node", "decommission", strconv.FormatInt(req.Identity.SQLNodeID, 10), "--dry-run", "--checks=strict"); err != nil {
			return err
		}
	}
	for _, i := range all {
		_, info, err := r.site(ctx, i)
		if err != nil {
			return err
		}
		if kind == "join" && i.DeploymentID == req.Identity.DeploymentID {
			if info.Enabled {
				return ErrConflict
			}
			p, _ := r.profile(i)
			u, _ := url.Parse(i.StorageEndpoint)
			c, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(p.AccessKey, p.SecretKey, ""), Secure: u.Scheme == "https", Transport: r.Transport})
			if err != nil {
				return ErrBlocked
			}
			b, err := c.ListBuckets(ctx)
			if err != nil {
				return ErrBlocked
			}
			if len(b) != 0 {
				return ErrBlocked
			}
		} else {
			want := all
			if kind == "join" {
				want = req.Peers
			}
			if !exact(info, want) {
				return ErrConflict
			}
		}
	}
	return nil
}
func (r *Remote) ChangeStorage(ctx context.Context, kind string, req Request) error {
	ctx, cancel := r.bounded(ctx)
	defer cancel()
	all, err := participants(req)
	if err != nil {
		return err
	}
	// Verify immutable deployment identity immediately before mutation.
	for _, i := range all {
		if _, _, err = r.site(ctx, i); err != nil {
			return err
		}
	}
	c, err := r.client(req.Peers[0])
	if err != nil {
		return err
	}
	if kind == "join" {
		sites := make([]madmin.PeerSite, 0, len(all))
		for _, i := range all {
			p, err := r.profile(i)
			if err != nil {
				return err
			}
			sites = append(sites, madmin.PeerSite{Name: i.SiteName, Endpoint: i.StorageEndpoint, AccessKey: p.AccessKey, SecretKey: p.SecretKey})
		}
		out, err := c.SiteReplicationAdd(ctx, sites, madmin.SRAddOptions{})
		if err != nil || !out.Success || out.InitialSyncErrorMessage != "" {
			return ErrBlocked
		}
		return nil
	}
	if kind != "retire" {
		return errors.New("invalid lifecycle operation")
	}
	out, err := c.SiteReplicationRemove(ctx, madmin.SRRemoveReq{SiteNames: []string{req.Identity.SiteName}, RemoveAll: false})
	if err != nil || out.Status != madmin.ReplicateRemoveStatusSuccess {
		return ErrBlocked
	}
	return nil
}

func (r *Remote) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func replacementIdentities(req Request) error {
	if req.PreviousIdentity == nil {
		return ErrConflict
	}
	old, new := *req.PreviousIdentity, req.Identity
	if old.DeploymentID == new.DeploymentID {
		return ErrConflict
	}
	old.DeploymentID = ""
	new.DeploymentID = ""
	if old != new {
		return ErrConflict
	}
	_, err := participants(req)
	return err
}

// OldStorageAbsent accepts only the expected survivor set, optionally still
// containing the old site. Unexpected deployments or changed endpoints block it.
func (r *Remote) OldStorageAbsent(ctx context.Context, req Request) (bool, error) {
	if err := replacementIdentities(req); err != nil {
		return false, err
	}
	if _, _, err := r.site(ctx, req.Identity); err != nil {
		return false, err
	}
	absent := true
	for _, peer := range req.Peers {
		_, info, err := r.site(ctx, peer)
		if err != nil {
			return false, err
		}
		if exact(info, req.Peers) {
			continue
		}
		withOld := append(append([]Identity{}, req.Peers...), *req.PreviousIdentity)
		withNew := append(append([]Identity{}, req.Peers...), req.Identity)
		if exact(info, withNew) {
			continue
		}
		if !exact(info, withOld) {
			return false, ErrConflict
		}
		absent = false
	}
	return absent, nil
}
func (r *Remote) RemoveOldStorage(ctx context.Context, req Request) error {
	ctx, cancel := r.bounded(ctx)
	defer cancel()
	if err := replacementIdentities(req); err != nil {
		return err
	}
	if err := r.verifySQL(ctx, req.Identity); err != nil {
		return err
	}
	if _, _, err := r.site(ctx, req.Identity); err != nil {
		return err
	}
	// A fresh replacement may not have buckets. Never erase one to satisfy this.
	p, err := r.profile(req.Identity)
	if err != nil {
		return err
	}
	u, _ := url.Parse(req.Identity.StorageEndpoint)
	c, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(p.AccessKey, p.SecretKey, ""), Secure: u.Scheme == "https", Transport: r.Transport})
	if err != nil {
		return ErrBlocked
	}
	buckets, err := c.ListBuckets(ctx)
	if err != nil || len(buckets) != 0 {
		return ErrBlocked
	}
	for _, peer := range req.Peers {
		admin, info, err := r.site(ctx, peer)
		if err != nil {
			return err
		}
		hasOld := false
		for _, site := range info.Sites {
			if site.DeploymentID == req.PreviousIdentity.DeploymentID {
				if site.Name != req.PreviousIdentity.SiteName {
					return ErrConflict
				}
				hasOld = true
			}
		}
		if !hasOld {
			continue
		}
		// The lost volume cannot acknowledge this. Reconcile each survivor even when
		// MinIO reports a partial failure, then demand an observed exact topology.
		_, _ = admin.SiteReplicationRemove(ctx, madmin.SRRemoveReq{SiteNames: []string{req.PreviousIdentity.SiteName}, RemoveAll: false})
	}
	absent, err := r.OldStorageAbsent(ctx, req)
	if err != nil {
		return err
	}
	if !absent {
		return ErrBlocked
	}
	return nil
}
