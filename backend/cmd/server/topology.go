package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/admin"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/config"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/lifecycle"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	madmin "github.com/minio/madmin-go/v3"
)

type nodeIdentity struct {
	NodeID           string `json:"node_id"`
	BackendEndpoint  string `json:"backend_endpoint"`
	DatabaseEndpoint string `json:"database_endpoint"`
	StorageEndpoint  string `json:"storage_endpoint"`
	ClusterID        string `json:"cluster_id"`
	SQLNodeID        int64  `json:"sql_node_id"`
	DeploymentID     string `json:"deployment_id"`
	SiteName         string `json:"site_name"`
}

func localIdentity(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) (nodeIdentity, error) {
	db, _ := url.Parse(cfg.DatabaseURL)
	db.User = nil
	db.RawQuery = ""
	db.ForceQuery = false
	identity := nodeIdentity{NodeID: cfg.NodeID, BackendEndpoint: cfg.BackendEndpoint, DatabaseEndpoint: db.String(), StorageEndpoint: cfg.S3Endpoint, SiteName: cfg.NodeID}
	if err := pool.QueryRow(ctx, "SELECT crdb_internal.cluster_id()::STRING,crdb_internal.node_id()").Scan(&identity.ClusterID, &identity.SQLNodeID); err != nil {
		return identity, err
	}
	u, _ := url.Parse(cfg.S3Endpoint)
	client, err := madmin.New(u.Host, cfg.S3AccessKey, cfg.S3SecretKey, u.Scheme == "https")
	if err != nil {
		return identity, err
	}
	info, err := client.ServerInfo(ctx)
	if err != nil {
		return identity, err
	}
	identity.DeploymentID = info.DeploymentID
	sites, err := client.SiteReplicationInfo(ctx)
	if err != nil {
		return identity, err
	}
	for _, site := range sites.Sites {
		if site.DeploymentID == identity.DeploymentID {
			identity.SiteName = site.Name
		}
	}
	if identity.DeploymentID == "" {
		return identity, cluster.ErrInvalid
	}
	return identity, nil
}

// Identity stays reachable before registration and without a bucket. The
// administrator must associate the empty MinIO site before normal admission.
func identityHandler(cfg config.Config, pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := sha256.Sum256([]byte("Bearer " + cfg.ControlToken))
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		identity, err := localIdentity(ctx, cfg, pool)
		if err != nil {
			http.Error(w, "identity unavailable", 503)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identity)
	})
}

func (i nodeIdentity) infrastructure(profile string) lifecycle.Identity {
	return lifecycle.Identity{ClusterID: i.ClusterID, SQLNodeID: i.SQLNodeID, DeploymentID: i.DeploymentID, SiteName: i.SiteName, StorageEndpoint: i.StorageEndpoint, CredentialProfile: profile}
}

func topologyManager(cfg config.Config, pool *pgxpool.Pool, registry *cluster.Store) (*admin.NodeManager, error) {
	db, _ := url.Parse(cfg.DatabaseURL)
	remote := &lifecycle.Remote{Profiles: map[string]lifecycle.Profile{"default": {SQLGateway: db.Host, Insecure: db.Query().Get("sslmode") == "disable", AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey}}}
	if cfg.AdminProfilesFile != "" {
		file, err := os.Open(cfg.AdminProfilesFile)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		var profiles map[string]struct {
			SQLGateway string `json:"sql_gateway"`
			CertsDir   string `json:"certs_dir"`
			Insecure   bool   `json:"insecure"`
			AccessKey  string `json:"access_key"`
			SecretKey  string `json:"secret_key"`
		}
		decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&profiles); err != nil {
			return nil, err
		}
		for name, p := range profiles {
			if name == "" || p.SQLGateway == "" || p.AccessKey == "" || p.SecretKey == "" || (!p.Insecure && p.CertsDir == "") {
				return nil, cluster.ErrInvalid
			}
			remote.Profiles[name] = lifecycle.Profile{SQLGateway: p.SQLGateway, CertsDir: p.CertsDir, Insecure: p.Insecure, AccessKey: p.AccessKey, SecretKey: p.SecretKey}
		}
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &admin.NodeManager{Lifecycle: &lifecycle.Service{Pool: pool, Adapter: remote}, Registry: registry, Discover: func(ctx context.Context, r cluster.Registration, profile string) (lifecycle.Identity, error) {
		if _, ok := remote.Profiles[profile]; !ok {
			return lifecycle.Identity{}, cluster.ErrInvalid
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.BackendEndpoint, "/")+"/internal/node/identity", nil)
		if err != nil {
			return lifecycle.Identity{}, cluster.ErrInvalid
		}
		req.Header.Set("Authorization", "Bearer "+cfg.ControlToken)
		resp, err := client.Do(req)
		if err != nil {
			return lifecycle.Identity{}, lifecycle.ErrBlocked
		}
		defer resp.Body.Close()
		var identity nodeIdentity
		if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&identity) != nil {
			return lifecycle.Identity{}, lifecycle.ErrBlocked
		}
		if identity.NodeID != r.NodeID || identity.BackendEndpoint != r.BackendEndpoint || identity.DatabaseEndpoint != r.DatabaseEndpoint || identity.StorageEndpoint != r.StorageEndpoint {
			return lifecycle.Identity{}, cluster.ErrConflict
		}
		return identity.infrastructure(profile), nil
	}}, nil
}

func runTopology(ctx context.Context, runtime *nodeRuntime, nodeID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		step, cancel := context.WithTimeout(ctx, 45*time.Second)
		check, stopCheck := context.WithTimeout(step, 3*time.Second)
		err := runtime.controller.Eligible(check)
		stopCheck()
		if err == nil {
			var lease cluster.Lease
			err = runtime.topology.Pool.QueryRow(step, `SELECT holder_id::STRING,term,expires_at FROM manager_lease WHERE singleton=true AND holder_id=$1 AND expires_at>clock_timestamp()`, nodeID).Scan(&lease.HolderID, &lease.Term, &lease.ExpiresAt)
			if err == nil {
				op, err := runtime.topology.Pending(step)
				if errors.Is(err, pgx.ErrNoRows) {
					op, err = runtime.topology.BeginPendingReplacement(step, lease)
				}
				if err == nil {
					_ = runtime.topology.Step(step, lease, op.ID)
				}
			}
		}
		cancel()
	}
}

func observeLocalIdentity(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, service *lifecycle.Service, node string) (lifecycle.IdentityObservation, error) {
	identity, err := localIdentity(ctx, cfg, pool)
	if err != nil {
		return lifecycle.IdentityObservation{}, err
	}
	observed := identity.infrastructure("default")
	var raw []byte
	err = pool.QueryRow(ctx, "SELECT identity FROM node_infrastructure WHERE node_id=$1", node).Scan(&raw)
	if err == nil {
		var confirmed lifecycle.Identity
		if err = json.Unmarshal(raw, &confirmed); err != nil {
			return lifecycle.IdentityObservation{}, err
		}
		// An empty replacement has no replication name yet. Keep the registered
		// name/profile while comparing actual deployment and SQL identities.
		observed.SiteName = confirmed.SiteName
		observed.CredentialProfile = confirmed.CredentialProfile
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return lifecycle.IdentityObservation{}, err
	}
	return service.ObserveIdentity(ctx, node, observed)
}

func watchIdentity(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, runtime *nodeRuntime, restart context.CancelFunc) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		observation, err := observeLocalIdentity(check, cfg, pool, runtime.topology, runtime.nodeID)
		cancel()
		if err == nil && (observation.Blocked || observation.StorageGeneration != runtime.storageGeneration) {
			restart()
			return
		}
		if errors.Is(err, lifecycle.ErrConflict) {
			restart()
			return
		}
	}
}
