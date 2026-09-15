package lifecycle

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
)

func TestJoinReplacementExecutionAndABA(t *testing.T) {
	original := Operation{ID: "same-job", NodeID: "node", Kind: "join", Stage: "storage", Request: request("new")}
	before, _ := json.Marshal(original)
	old := original.Request.Identity
	observed := old
	observed.DeploymentID = "replacement"
	adapted, err := replacementExecution(original, old, observed, "generation-2", nil)
	if err != nil || adapted.ID != original.ID || adapted.Kind != "join" || adapted.Stage != "remove_old" {
		t.Fatalf("adaptation %+v %v", adapted, err)
	}
	after, _ := json.Marshal(original)
	if string(after) != string(before) {
		t.Fatal("original request changed")
	}
	id := original.ID
	if sameExecution(original, "generation-2", &id, observed) {
		t.Fatal("old response accepted after storage/remove_old/storage ABA")
	}
	if !sameExecution(adapted, "generation-2", &id, observed) {
		t.Fatal("current execution rejected")
	}
	if sameExecution(adapted, "generation-3", &id, observed) {
		t.Fatal("different generation accepted")
	}
	other := "other-job"
	if _, err = replacementExecution(original, old, observed, "generation-2", &other); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign attachment accepted")
	}
	original.Stage = "sql"
	if _, err = replacementExecution(original, old, observed, "generation-2", nil); !errors.Is(err, ErrConflict) {
		t.Fatal("unexpected phase rewritten")
	}
}
func TestJoinResumesAfterVolumeReplacement(t *testing.T) {
	for _, phase := range []string{"preflight", "storage"} {
		t.Run(phase, func(t *testing.T) {
			ctx, pool := setup(t)
			store := cluster.New(pool)
			manager, err := store.Register(ctx, reg("manager"))
			if err != nil {
				t.Fatal(err)
			}
			lease, err := store.AcquireLease(ctx, manager.ID, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			adapter := &replacementFake{}
			service := &Service{Pool: pool, Adapter: adapter}
			req := request("new")
			op, err := service.BeginJoin(ctx, lease, reg("new"), req, "original-key")
			if err != nil {
				t.Fatal(err)
			}
			if phase == "storage" {
				if err = service.Step(ctx, lease, op.ID); err != nil {
					t.Fatal(err)
				}
			}
			observed := req.Identity
			observed.DeploymentID = "replacement"
			if _, err = service.ObserveIdentity(ctx, op.NodeID, observed); err != nil {
				t.Fatal(err)
			}
			if err = service.Step(ctx, lease, op.ID); err != nil {
				t.Fatal(err)
			}
			stored, err := service.Get(ctx, op.ID)
			if err != nil || stored.Stage != "storage" || stored.Kind != "join" || stored.Request.Identity != req.Identity || stored.Request.PreviousIdentity != nil {
				t.Fatalf("original intent altered %+v %v", stored, err)
			}
			replay, err := service.BeginJoin(ctx, lease, reg("new"), req, "original-key")
			if err != nil || replay.ID != op.ID {
				t.Fatalf("idempotency lost %+v %v", replay, err)
			}
			if _, err = service.BeginJoin(ctx, lease, reg("other"), request("other"), "other-key"); !errors.Is(err, ErrConflict) {
				t.Fatal("topology slot released early")
			}
			if err = store.StartSync(ctx, lease, op.NodeID); !errors.Is(err, cluster.ErrConflict) {
				t.Fatal("gate released before association")
			}
			if err = service.Step(ctx, lease, op.ID); err != nil {
				t.Fatal(err)
			}
			stored, err = service.Get(ctx, op.ID)
			if err != nil || stored.Stage != "complete" || stored.Request.Identity != req.Identity {
				t.Fatalf("completion %+v %v", stored, err)
			}
			if err = store.StartSync(ctx, lease, op.NodeID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Admit(ctx, lease, op.NodeID, 0); err != nil {
				t.Fatal(err)
			}
			if adapter.removes != 1 || adapter.storageCalls != 1 {
				t.Fatalf("external sequence %+v", adapter)
			}
			t.Log("same join ID/request/key preserved; slot held; replacement associated before synchronization")
		})
	}
}
