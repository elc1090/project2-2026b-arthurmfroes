// Package control runs the node manager independently from browsers and panels.
package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Pool                        *pgxpool.Pool
	Store                       *cluster.Store
	Local                       cluster.Node
	Token                       string
	StorageProbe                func(context.Context) error
	LocalGate                   func(context.Context, string) error
	Sync                        func(context.Context, cluster.Node, cluster.RecoveryPlan) error
	Interval, Timeout, LeaseTTL time.Duration
	FailureThreshold            int
	HTTPClient                  *http.Client
}

type Controller struct {
	cfg      Config
	mu       sync.Mutex
	lease    cluster.Lease
	failures map[string]int
	job      *syncJob
}
type syncJob struct {
	node   string
	done   chan error
	cancel context.CancelFunc
}

type Health struct {
	Backend bool `json:"backend"`
	Control bool `json:"control"`
	SQL     bool `json:"sql"`
	Storage bool `json:"storage"`
}

func (h Health) Healthy() bool { return h.Backend && h.Control && h.SQL && h.Storage }

type ClusterView struct {
	Nodes         []cluster.Node   `json:"nodes"`
	Configuration cluster.Snapshot `json:"configuration"`
}

func New(cfg Config) (*Controller, error) {
	if cfg.Pool == nil || cfg.Store == nil || cfg.Local.ID == "" || cfg.Token == "" || cfg.StorageProbe == nil || cfg.Interval <= 0 || cfg.Timeout <= 0 || cfg.LeaseTTL <= cfg.Interval+cfg.Timeout || cfg.FailureThreshold < 1 {
		return nil, errors.New("invalid control configuration")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	client := *cfg.HTTPClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	cfg.HTTPClient = &client
	return &Controller{cfg: cfg, failures: map[string]int{}}, nil
}

func (c *Controller) health(ctx context.Context) Health {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	h := Health{Backend: c.gate(ctx, "backend") == nil, Control: c.gate(ctx, "control") == nil}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.SQL = c.gate(ctx, "sql") == nil && database.WithTx(ctx, c.cfg.Pool, func(tx pgx.Tx) error {
			var version int64
			return tx.QueryRow(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true").Scan(&version)
		}) == nil
	}()
	go func() { defer wg.Done(); h.Storage = c.gate(ctx, "storage") == nil && c.cfg.StorageProbe(ctx) == nil }()
	wg.Wait()
	return h
}

// Eligible is a preflight, not a substitute for cluster.Guard inside mutations.
func (c *Controller) Eligible(ctx context.Context) error {
	if !c.health(ctx).Healthy() {
		return cluster.ErrNoAuthority
	}
	return database.WithTx(ctx, c.cfg.Pool, func(tx pgx.Tx) error { return cluster.Guard(ctx, tx, c.cfg.Local.ID) })
}

// Run continues after transient failures. Lack of SQL authority fails closed;
// local health and the elected manager's next pass determine recovery.
func (c *Controller) Run(ctx context.Context) error {
	timer := time.NewTicker(c.cfg.Interval)
	defer timer.Stop()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.job != nil {
			c.job.cancel()
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		_ = c.Step(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (c *Controller) Step(ctx context.Context) error {
	recoveryContext := ctx
	ctx, cancel := context.WithTimeout(ctx, c.cfg.LeaseTTL)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.health(ctx).Healthy() {
		c.lease = cluster.Lease{}
		if c.job != nil {
			c.job.cancel()
		}
		return cluster.ErrNoAuthority
	}
	var err error
	if c.lease.HolderID != "" {
		c.lease, err = c.cfg.Store.RenewLease(ctx, c.lease, c.cfg.LeaseTTL)
		if err != nil {
			c.lease = cluster.Lease{}
			if c.job != nil {
				c.job.cancel()
			}
		}
	}
	if c.lease.HolderID == "" {
		c.lease, err = c.cfg.Store.AcquireLease(ctx, c.cfg.Local.ID, c.cfg.LeaseTTL)
		if err != nil {
			return err
		}
		c.failures = map[string]int{}
	}
	nodes, err := c.cfg.Store.Nodes(ctx)
	if err != nil {
		return err
	}
	// Probe all nodes concurrently so one timeout does not multiply by cluster size.
	type result struct {
		node        cluster.Node
		failure     string
		observation cluster.Observation
	}
	results := make(chan result, len(nodes))
	var wg sync.WaitGroup
	for _, node := range nodes {
		if node.State == "removed" {
			continue
		}
		wg.Add(1)
		go func(n cluster.Node) {
			defer wg.Done()
			observation, failure := c.remoteObservation(ctx, n)
			results <- result{node: n, failure: failure, observation: observation}
		}(node)
	}
	wg.Wait()
	close(results)
	for result := range results {
		node := result.node
		if err := c.cfg.Store.RecordObservation(ctx, c.lease, node.ID, result.observation); err != nil {
			return err
		}
		if result.failure != "" {
			if c.job != nil && c.job.node == node.ID {
				c.job.cancel()
			}
			c.failures[node.ID]++
			if c.failures[node.ID] >= c.cfg.FailureThreshold {
				if _, err := c.cfg.Store.Exclude(ctx, c.lease, node.ID, result.failure); err != nil {
					return err
				}
				if node.ID == c.cfg.Local.ID {
					c.lease = cluster.Lease{}
					if c.job != nil {
						c.job.cancel()
					}
					return cluster.ErrNoAuthority
				}
			}
			continue
		}
		delete(c.failures, node.ID)
		if node.State == "ready" {
			continue
		}
		if err := c.cfg.Store.StartSync(ctx, c.lease, node.ID); err != nil {
			// A blocked or concurrently changed node must not stop failure
			// detection for the remaining nodes in this pass.
			if errors.Is(err, cluster.ErrConflict) {
				continue
			}
			return err
		}
		plan, err := c.cfg.Store.AdmissionPlan(ctx, node.ID)
		if err != nil {
			if errors.Is(err, cluster.ErrConflict) {
				continue
			}
			return err
		}
		if len(plan.Files) > 0 {
			if c.cfg.Sync == nil {
				continue
			}
			if !c.syncComplete(recoveryContext, plan) {
				continue
			}
		}
		if _, err := c.cfg.Store.Admit(ctx, c.lease, node.ID, plan.PublicationGeneration); err != nil && !errors.Is(err, cluster.ErrSyncRequired) && !errors.Is(err, cluster.ErrConflict) {
			return fmt.Errorf("node admission: %w", err)
		}
	}
	return nil
}

// At most one recovery callback runs per controller. It never owns the manager
// loop or SQL locks. Callbacks must honor cancellation; a stuck callback prevents
// another recovery worker, but cannot prevent health detection or lease renewal.
func (c *Controller) syncComplete(ctx context.Context, plan cluster.RecoveryPlan) bool {
	if c.job != nil {
		select {
		case err := <-c.job.done:
			same := c.job.node == plan.Node.ID
			c.job.cancel()
			c.job = nil
			if same && err == nil {
				return true
			}
		default:
			return false
		}
	}
	workCtx, cancel := context.WithCancel(ctx)
	job := &syncJob{node: plan.Node.ID, done: make(chan error, 1), cancel: cancel}
	c.job = job
	go func() { job.done <- c.cfg.Sync(workCtx, plan.Node, plan) }()
	return false
}

func (c *Controller) gate(ctx context.Context, component string) error {
	if c.cfg.LocalGate == nil {
		return nil
	}
	return c.cfg.LocalGate(ctx, component)
}
