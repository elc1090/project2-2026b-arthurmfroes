package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

func (c *Controller) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/probe", func(w http.ResponseWriter, r *http.Request) {
		h := c.health(r.Context())
		status := http.StatusOK
		if !h.Healthy() {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, h)
	})
	mux.HandleFunc("GET /internal/cluster", func(w http.ResponseWriter, r *http.Request) {
		if c.gate(r.Context(), "backend") != nil || c.gate(r.Context(), "control") != nil || c.gate(r.Context(), "sql") != nil {
			http.Error(w, "control unavailable", http.StatusServiceUnavailable)
			return
		}
		view, err := c.authoritativeView(r.Context())
		if err != nil {
			http.Error(w, "control unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := sha256.Sum256([]byte("Bearer " + c.cfg.Token))
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (c *Controller) remoteObservation(ctx context.Context, node cluster.Node) (cluster.Observation, string) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	available, unavailable := true, false
	obs := cluster.Observation{Backend: &unavailable, Control: &unavailable}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(node.BackendEndpoint, "/")+"/internal/probe", nil)
	if err != nil {
		return obs, "backend/control probe unavailable"
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return obs, "backend/control probe unavailable"
	}
	defer resp.Body.Close()
	obs.Backend = &available
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		return obs, "control probe unavailable"
	}
	var health Health
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health) != nil {
		return obs, "control probe unavailable"
	}
	obs = cluster.Observation{Backend: &health.Backend, SQL: &health.SQL, Storage: &health.Storage, Control: &health.Control}
	if !health.Backend {
		return obs, "backend probe failed"
	}
	if !health.Control {
		return obs, "control probe failed"
	}
	if !health.SQL {
		return obs, "SQL probe failed"
	}
	if !health.Storage {
		return obs, "storage probe failed"
	}
	if resp.StatusCode != http.StatusOK {
		return obs, "control probe unavailable"
	}
	return obs, ""
}

// Read the routing snapshot and remaining authority in one serializable view.
// Clients subtract transport time from ValidForMillis before caching it.
func (c *Controller) authoritativeView(ctx context.Context) (struct {
	ClusterView
	ValidForMillis int64 `json:"valid_for_ms"`
}, error) {
	var result struct {
		ClusterView
		ValidForMillis int64 `json:"valid_for_ms"`
	}
	err := database.WithTx(ctx, c.cfg.Pool, func(tx pgx.Tx) error {
		result.ClusterView = ClusterView{Nodes: []cluster.Node{}, Configuration: cluster.Snapshot{Members: []cluster.Node{}}}
		if err := tx.QueryRow(ctx, "SELECT version,publication_generation FROM cluster_configuration WHERE singleton=true").Scan(&result.Configuration.Version, &result.Configuration.PublicationGeneration); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT n.id::STRING,n.node_id,n.backend_endpoint,n.database_endpoint,n.storage_endpoint,n.storage_generation::STRING,n.state,m.node_id IS NOT NULL FROM cluster_nodes n LEFT JOIN cluster_membership m ON n.id=m.node_id ORDER BY n.node_id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n cluster.Node
			var member bool
			if err := rows.Scan(&n.ID, &n.NodeID, &n.BackendEndpoint, &n.DatabaseEndpoint, &n.StorageEndpoint, &n.StorageGeneration, &n.State, &member); err != nil {
				rows.Close()
				return err
			}
			result.Nodes = append(result.Nodes, n)
			if member {
				result.Configuration.Members = append(result.Configuration.Members, n)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT floor(extract(epoch FROM (expires_at-clock_timestamp()))*1000)::INT8 FROM manager_lease WHERE singleton=true AND holder_id IS NOT NULL AND expires_at>clock_timestamp()`).Scan(&result.ValidForMillis); err != nil {
			return err
		}
		if result.ValidForMillis <= 0 {
			return cluster.ErrNoAuthority
		}
		return nil
	})
	return result, err
}
