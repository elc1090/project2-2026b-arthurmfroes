// Package admin provides operational projections without private file metadata.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	Nodes        *NodeManager
	Pool         *pgxpool.Pool
	FaultControl FaultControl
}
type Node struct {
	ID               string          `json:"id"`
	NodeID           string          `json:"node_id"`
	State            string          `json:"state"`
	Routed           bool            `json:"routed"`
	Health           json.RawMessage `json:"health"`
	Reason           *string         `json:"reason"`
	ObservedAt       *time.Time      `json:"observed_at"`
	TransitionedAt   time.Time       `json:"transitioned_at"`
	SyncedGeneration int64           `json:"synced_generation"`
}
type Site struct {
	ID        string `json:"id"`
	NodeID    string `json:"node_id"`
	Confirmed bool   `json:"confirmed"`
}
type Transfer struct {
	Sites          []Site  `json:"sites"`
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	Phase          string  `json:"phase"`
	Parts          int64   `json:"parts"`
	ReceivedParts  int64   `json:"received_parts"`
	Copies         int64   `json:"copies"`
	RequiredCopies int64   `json:"required_copies"`
	ErrorCode      *string `json:"error_code"`
}
type Event struct {
	ID                   string            `json:"id"`
	NodeID               *string           `json:"node_id"`
	Kind                 string            `json:"kind"`
	ConfigurationVersion int64             `json:"configuration_version"`
	ManagerTerm          int64             `json:"manager_term"`
	Details              map[string]string `json:"details"`
	At                   time.Time         `json:"at"`
}
type View struct {
	Nodes                 []Node        `json:"nodes"`
	Operations            []Transfer    `json:"operations"`
	Events                []Event       `json:"events"`
	Version               int64         `json:"version"`
	PublicationGeneration int64         `json:"publication_generation"`
	ManagerID             *string       `json:"manager_id"`
	ManagerTerm           int64         `json:"manager_term"`
	LeaseExpiresAt        *time.Time    `json:"lease_expires_at"`
	ObservedAt            time.Time     `json:"observed_at"`
	FaultActions          []FaultAction `json:"fault_actions"`
	FaultControlAvailable bool          `json:"fault_control_available"`
	FaultControlError     *string       `json:"fault_control_error"`
}

func (s Service) View(ctx context.Context) (View, error) {
	var result View
	err := database.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		result = View{Nodes: []Node{}, Operations: []Transfer{}, Events: []Event{}, FaultActions: []FaultAction{}}
		if err := tx.QueryRow(ctx, "SELECT c.version,c.publication_generation,l.holder_id::STRING,l.term,l.expires_at,clock_timestamp() FROM cluster_configuration c CROSS JOIN manager_lease l WHERE c.singleton AND l.singleton").Scan(&result.Version, &result.PublicationGeneration, &result.ManagerID, &result.ManagerTerm, &result.LeaseExpiresAt, &result.ObservedAt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT n.id::STRING,n.node_id,n.state,m.node_id IS NOT NULL,n.health,n.reason,n.observed_at,n.transitioned_at,n.synced_publication_generation FROM cluster_nodes n LEFT JOIN cluster_membership m ON m.node_id=n.id ORDER BY n.node_id")
		if err != nil {
			return err
		}
		for rows.Next() {
			var n Node
			if err = rows.Scan(&n.ID, &n.NodeID, &n.State, &n.Routed, &n.Health, &n.Reason, &n.ObservedAt, &n.TransitionedAt, &n.SyncedGeneration); err != nil {
				rows.Close()
				return err
			}
			result.Nodes = append(result.Nodes, n)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT u.id::STRING,u.status,u.phase,u.part_count,
   (SELECT count(DISTINCT p.part_index) FROM upload_part_copies p JOIN cluster_nodes n ON n.id=p.node_id AND n.storage_generation=p.storage_generation WHERE p.operation_id=u.id),
   (SELECT count(*) FROM object_copies c JOIN cluster_nodes n ON n.id=c.node_id AND n.storage_generation=c.storage_generation JOIN cluster_membership m ON m.node_id=n.id WHERE c.operation_id=u.id),
   (SELECT count(*) FROM cluster_membership),u.error_code FROM upload_operations u WHERE u.status='pending' ORDER BY u.created_at,u.id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var op Transfer
			if err = rows.Scan(&op.ID, &op.Status, &op.Phase, &op.Parts, &op.ReceivedParts, &op.Copies, &op.RequiredCopies, &op.ErrorCode); err != nil {
				rows.Close()
				return err
			}
			result.Operations = append(result.Operations, op)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for i := range result.Operations {
			op := &result.Operations[i]
			op.Sites = []Site{}
			sites, err := tx.Query(ctx, `SELECT n.id::STRING,n.node_id,c.operation_id IS NOT NULL FROM cluster_membership m JOIN cluster_nodes n ON n.id=m.node_id LEFT JOIN object_copies c ON c.node_id=n.id AND c.storage_generation=n.storage_generation AND c.operation_id=$1 ORDER BY n.node_id`, op.ID)
			if err != nil {
				return err
			}
			for sites.Next() {
				var site Site
				if err := sites.Scan(&site.ID, &site.NodeID, &site.Confirmed); err != nil {
					sites.Close()
					return err
				}
				op.Sites = append(op.Sites, site)
			}
			sites.Close()
			if err = sites.Err(); err != nil {
				return err
			}
		}
		rows, err = tx.Query(ctx, "SELECT id::STRING,node_id::STRING,kind,configuration_version,manager_term,details,created_at FROM cluster_events ORDER BY created_at DESC,id DESC LIMIT 100")
		if err != nil {
			return err
		}
		for rows.Next() {
			var e Event
			var details []byte
			if err = rows.Scan(&e.ID, &e.NodeID, &e.Kind, &e.ConfigurationVersion, &e.ManagerTerm, &details, &e.At); err != nil {
				rows.Close()
				return err
			}
			e.Details = safeEventDetails(details)
			result.Events = append(result.Events, e)
		}
		rows.Close()
		return rows.Err()
	})
	if err != nil {
		return result, err
	}
	if s.FaultControl == nil {
		message := "Controle de falhas não configurado."
		result.FaultControlError = &message
		return result, nil
	}
	actions, err := s.FaultControl.Actions(ctx)
	if err != nil {
		message := "Controle de falhas indisponível."
		result.FaultControlError = &message
		return result, nil
	}
	result.FaultActions = actions
	result.FaultControlAvailable = true
	return result, nil
}

func safeEventDetails(raw []byte) map[string]string {
	result := map[string]string{}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return result
	}
	allowed := map[string]bool{"reason": true, "stage": true, "operation": true, "previous_deployment": true, "observed_deployment": true, "action": true, "component": true, "status": true}
	for key, value := range input {
		if !allowed[key] {
			continue
		}
		switch value := value.(type) {
		case string:
			if len(value) <= 256 {
				result[key] = value
			}
		case bool:
			result[key] = fmt.Sprint(value)
		}
	}
	return result
}
