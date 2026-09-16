package uploads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
)

type delayedPart struct {
	started, release chan struct{}
	once             sync.Once
	body             io.Reader
}

func TestRealDeletedFileCleanupRetriesAndPreservesOpenDownload(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000091", "deleted.bin")
	if _, err := services[0].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := services[0].Get(ctx, owner, op.ID)
	if err != nil || op.FileID == nil {
		t.Fatal(op, err)
	}
	opened, _, _, err := services[0].Download(ctx, owner, *op.FileID)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := (catalog.Service{Pool: pool}).DeleteFile(ctx, owner, *op.FileID)
	if err != nil || deletion.OperationID != op.ID {
		t.Fatal(deletion, err)
	}
	if _, _, _, err := services[0].Download(ctx, owner, *op.FileID); !errors.Is(err, ErrNotFound) {
		t.Fatal("new download admitted after tombstone", err)
	}

	ambiguous := errors.New("injected ambiguous delete response")
	target := services[2].cfg.LocalNode.ID
	originalFactory := services[0].cfg.StorageFor
	services[0].cfg.StorageFor = func(node cluster.Node) (*storage.Store, error) {
		if node.ID == target {
			return storage.New(storage.Options{Endpoint: node.StorageEndpoint, Bucket: "drive-clone", AccessKey: "minioadmin", SecretKey: "minioadmin", BeforeOperation: func(context.Context) error { return ambiguous }})
		}
		return originalFactory(node)
	}
	removed, err := services[0].CleanupDeletedFiles(ctx)
	if removed != 2 || !errors.Is(err, ambiguous) {
		t.Fatalf("first cleanup removed=%d err=%v", removed, err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM object_copies WHERE operation_id=$1", op.ID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("ambiguous removal retired receipt", receipts, err)
	}
	services[0].cfg.StorageFor = originalFactory
	if removed, err = services[0].CleanupDeletedFiles(ctx); err != nil || removed != 1 {
		t.Fatalf("retry removed=%d err=%v", removed, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM object_copies WHERE operation_id=$1", op.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("retry retained receipt", receipts, err)
	}
	body, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil || closeErr != nil || string(body) != "onetwo" {
		t.Fatal("opened download did not finish", string(body), readErr, closeErr)
	}
}

func TestRealDeletedFileCleanupDoesNotTargetReplacementGeneration(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000092", "generation.bin")
	if _, err := services[0].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := services[0].Get(ctx, owner, op.ID)
	if err != nil || op.FileID == nil {
		t.Fatal(op, err)
	}
	if _, err := (catalog.Service{Pool: pool}).DeleteFile(ctx, owner, *op.FileID); err != nil {
		t.Fatal(err)
	}
	target := services[2].cfg.LocalNode.ID
	if _, err := pool.Exec(ctx, "UPDATE cluster_nodes SET storage_generation=gen_random_uuid() WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	originalFactory := services[0].cfg.StorageFor
	targetedReplacement := false
	services[0].cfg.StorageFor = func(node cluster.Node) (*storage.Store, error) {
		if node.ID == target {
			targetedReplacement = true
		}
		return originalFactory(node)
	}
	removed, err := services[0].CleanupDeletedFiles(ctx)
	if err != nil || removed != 2 || targetedReplacement {
		t.Fatalf("removed=%d replacement_targeted=%v err=%v", removed, targetedReplacement, err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM object_copies WHERE operation_id=$1", op.ID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("stale-generation receipt was retired", receipts, err)
	}
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
