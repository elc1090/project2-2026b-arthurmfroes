package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/accounts"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/admin"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/api"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/config"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/control"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/lifecycle"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/uploads"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type runtimeState struct {
	current  atomic.Pointer[nodeRuntime]
	identity http.Handler
}
type nodeRuntime struct {
	nodeID            string
	storageGeneration string
	topology          *lifecycle.Service
	transfers         *uploads.Service
	controller        *control.Controller
	api, internal     http.Handler
}

// Startup retries local dependencies without preventing the process from exposing
// liveness. A failed bootstrap never creates a second database cluster.
func initialize(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, localStorage *storage.Store, state *runtimeState) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		runtime, err := prepareRuntime(attempt, cfg, pool, localStorage)
		cancel()
		if err == nil {
			state.current.Store(runtime)
			runCtx, stopRuntime := context.WithCancel(ctx)
			var workers sync.WaitGroup
			for _, work := range []func(context.Context){runtime.transfers.Run, runtime.transfers.RunCleanup, func(ctx context.Context) { runTopology(ctx, runtime, runtime.nodeID) }, func(ctx context.Context) { _ = runtime.controller.Run(ctx) }, func(ctx context.Context) { watchIdentity(ctx, cfg, pool, runtime, stopRuntime) }} {
				workers.Add(1)
				go func(work func(context.Context)) { defer workers.Done(); work(runCtx) }(work)
			}
			<-runCtx.Done()
			state.current.Store(nil)
			stopRuntime()
			workers.Wait()
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
		// Endpoint errors can contain credentials; log only the initialization stage.
		slog.Warn("node initialization pending", "node", cfg.NodeID)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func prepareRuntime(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, localStorage *storage.Store) (*nodeRuntime, error) {
	if err := database.Migrate(ctx, pool); err != nil {
		return nil, err
	}
	if cfg.AdminLogin != "" {
		if err := (accounts.Service{Pool: pool}).EnsureAdmin(ctx, cfg.AdminLogin, cfg.AdminPassword); err != nil {
			return nil, err
		}
	}
	dbEndpoint, _ := url.Parse(cfg.DatabaseURL)
	dbEndpoint.User = nil
	dbEndpoint.RawQuery = ""
	dbEndpoint.ForceQuery = false
	registry := cluster.New(pool)
	if !cfg.BootstrapNode {
		var registered bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM cluster_nodes WHERE node_id=$1)", cfg.NodeID).Scan(&registered); err != nil {
			return nil, err
		}
		if !registered {
			return nil, cluster.ErrNoAuthority
		}
	}
	node, err := registry.Register(ctx, cluster.Registration{NodeID: cfg.NodeID, BackendEndpoint: cfg.BackendEndpoint, DatabaseEndpoint: dbEndpoint.String(), StorageEndpoint: cfg.S3Endpoint})
	if err != nil {
		return nil, err
	}
	topology, err := topologyManager(cfg, pool, registry)
	if err != nil {
		return nil, err
	}
	observation, err := observeLocalIdentity(ctx, cfg, pool, topology.Lifecycle, node.ID)
	if err != nil {
		return nil, err
	}
	if observation.Blocked {
		return nil, lifecycle.ErrBlocked
	}
	if node.StorageGeneration != observation.StorageGeneration {
		return nil, lifecycle.ErrBlocked
	}
	var transfers *uploads.Service
	controller, err := control.New(control.Config{Sync: func(ctx context.Context, target cluster.Node, plan cluster.RecoveryPlan) error {
		return transfers.SyncPublished(ctx, target, plan)
	}, Pool: pool, Store: registry, Local: node, Token: cfg.ControlToken, StorageProbe: localStorage.Check, Interval: cfg.ControlInterval, Timeout: cfg.ControlTimeout, LeaseTTL: cfg.LeaseTTL, FailureThreshold: cfg.FailureThreshold})
	if err != nil {
		return nil, err
	}
	guard := func(ctx context.Context, tx pgx.Tx) error {
		return cluster.Guard(ctx, tx, node.ID)
	}
	storageFor := func(peer cluster.Node) (*storage.Store, error) {
		return storage.New(storage.Options{Endpoint: peer.StorageEndpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket})
	}
	gatedStorage, err := storageFor(node)
	if err != nil {
		return nil, err
	}
	transfers, err = uploads.New(uploads.ServiceConfig{Pool: pool, LocalNode: node, LocalStorage: gatedStorage, Cluster: registry, Eligible: controller.Eligible, StorageFor: storageFor})
	if err != nil {
		return nil, err
	}
	var faultControl admin.FaultControl
	if cfg.FaultActuatorURL != "" {
		faultControl, err = admin.NewHTTPFaultControl(cfg.FaultActuatorURL, cfg.FaultActuatorToken)
		if err != nil {
			return nil, err
		}
	}
	private := &api.API{Admin: &admin.Service{Pool: pool, FaultControl: faultControl, Nodes: topology}, Uploads: transfers, Accounts: accounts.Service{Pool: pool, Guard: guard}, Catalog: catalog.Service{Pool: pool, Guard: guard}, SecureCookies: cfg.SecureCookies, Eligible: func(r *http.Request) error {
		check, cancel := context.WithTimeout(r.Context(), cfg.ControlTimeout)
		defer cancel()
		return controller.Eligible(check)
	}}
	internal := http.NewServeMux()
	controlHandler := controller.Handler()
	internal.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := sha256.Sum256([]byte("Bearer " + cfg.ControlToken))
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		controlHandler.ServeHTTP(w, r)
	}))
	return &nodeRuntime{nodeID: node.ID, storageGeneration: node.StorageGeneration, topology: topology.Lifecycle, transfers: transfers, controller: controller, api: private.Handler(), internal: internal}, nil
}
