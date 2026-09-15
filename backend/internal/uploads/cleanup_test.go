package uploads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"io"
	"sync"
	"testing"
)

type delayedPart struct {
	started, release chan struct{}
	once             sync.Once
	body             io.Reader
}

func (r *delayedPart) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	return r.body.Read(p)
}

func TestRealTerminalCleanup(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	published := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000081", "cleanup-published")
	if _, err := services[0].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	published, err := services[0].Get(ctx, owner, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	active := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000082", "cleanup-active")
	cancelled, err := services[0].Create(ctx, owner, inputFor("00000000-0000-0000-0000-000000000083"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &delayedPart{started: make(chan struct{}), release: make(chan struct{}), body: bytes.NewBufferString("one")}
	done := make(chan error, 1)
	go func() { done <- services[0].PutPart(ctx, owner, cancelled.ID, 0, reader) }()
	<-reader.started
	if _, err := services[0].Cancel(ctx, owner, cancelled.ID); err != nil {
		close(reader.release)
		t.Fatal(err)
	}
	if _, err := services[0].Cleanup(ctx); err != nil {
		close(reader.release)
		t.Fatal(err)
	}
	close(reader.release)
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatal("late PUT recorded receipt", err)
	}
	var orphan storage.Receipt
	err = services[0].cfg.LocalStorage.VisitPartVersions(ctx, cancelled.ID, func(key, version string) error { orphan.Key = key; orphan.VersionID = version; return nil })
	if err != nil || orphan.Key == "" {
		t.Fatal("expected late orphan", orphan, err)
	}
	count, err := services[0].Cleanup(ctx)
	if err != nil || count < 1 {
		t.Fatal(count, err)
	}
	var remains int
	if err := services[0].cfg.LocalStorage.VisitPartVersions(ctx, cancelled.ID, func(string, string) error { remains++; return nil }); err != nil || remains != 0 {
		t.Fatal("orphan remains", remains, err)
	}
	for _, service := range services {
		if _, err := service.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
		r, _, _, err := service.Download(ctx, owner, *published.FileID)
		if err != nil {
			t.Fatal("final removed", err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil || string(body) != "onetwo" {
			t.Fatal(string(body), err)
		}
	}
	current, err := services[0].Get(ctx, owner, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range current.Parts {
		if !part.Available {
			t.Fatal("active part removed", part)
		}
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM upload_part_copies WHERE operation_id=$1`, published.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("terminal receipts retained", receipts, err)
	}
	for _, key := range []string{"objects/" + published.ID + "/hash", "parts/" + cancelled.ID + "-other/hash"} {
		if err := services[0].cfg.LocalStorage.RemovePartVersion(ctx, cancelled.ID, key, orphan.VersionID); !errors.Is(err, storage.ErrInvalid) {
			t.Fatal(key, err)
		}
	}
	if err := services[0].cfg.LocalStorage.VisitPartVersions(ctx, "../", func(string, string) error { return nil }); !errors.Is(err, storage.ErrInvalid) {
		t.Fatal(err)
	}
}

// No SQL receipt can appear after cancellation. Listing must still revisit each
// namespace, but empty namespaces need only the two page transactions.
func TestRealCleanedOperationsSkipReceiptTransactions(t *testing.T) {
	ctx, _, services, owner, _ := uploadFixture(t)
	for index := 1; index <= 3; index++ {
		op, err := services[0].Create(ctx, owner, CreateInput{IdempotencyKey: fmt.Sprintf("00000000-0000-0000-0000-%012d", 900+index), Name: fmt.Sprintf("never-uploaded-%d", index), SHA256: sha("")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = services[0].Cancel(ctx, owner, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	original := services[0].cfg.Eligible
	services[0].cfg.Eligible = func(ctx context.Context) error { calls++; return original(ctx) }
	for round := 0; round < 2; round++ {
		calls = 0
		removed, err := services[0].Cleanup(ctx)
		if err != nil || removed != 0 {
			t.Fatal(removed, err)
		}
		if calls != 2 {
			t.Fatalf("round %d: got %d metadata preflights; expected only two page transactions", round, calls)
		}
	}
}
