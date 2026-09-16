package uploads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/admin"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func createReceived(t *testing.T, ctx context.Context, services []*Service, owner, key, name string) Operation {
	t.Helper()
	input := inputFor(key)
	input.Name = name
	op, err := services[0].Create(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{"one", "two"} {
		if err := services[i].PutPart(ctx, owner, op.ID, int64(i), bytes.NewBufferString(data)); err != nil {
			t.Fatal(err)
		}
	}
	return op
}
func TestRealPublicationDownloadAndRecovery(t *testing.T) {
	ctx, pool, services, owner, other := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000010", "published.bin")
	if _, _, _, err := services[0].Download(ctx, owner, op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("pending operation exposed as file", err)
	}
	if found, err := services[2].processNext(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	current, err := services[0].Get(ctx, owner, op.ID)
	if err != nil || current.Status != "available" || current.FileID == nil {
		t.Fatal(current, err)
	}
	for _, s := range services {
		r, name, size, err := s.Download(ctx, owner, *current.FileID)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || name != "published.bin" || size != 6 || string(body) != "onetwo" {
			t.Fatal(name, size, string(body), err)
		}
	}
	if _, _, _, err := services[0].Download(ctx, other, *current.FileID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign download allowed", err)
	}
	input := inputFor(op.IdempotencyKey)
	input.Name = op.Name
	repeated, err := services[1].Create(ctx, owner, input)
	if err != nil || repeated.FileID == nil || *repeated.FileID != *current.FileID {
		t.Fatal("lost response duplicate", repeated, err)
	}
	cancelled, err := services[1].Cancel(ctx, owner, op.ID)
	if err != nil || cancelled.Status != "available" {
		t.Fatal("cancel removed published file", cancelled, err)
	}
	var count, generation int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM object_copies WHERE operation_id=$1", op.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if err = pool.QueryRow(ctx, "SELECT publication_generation FROM cluster_configuration WHERE singleton=true").Scan(&generation); err != nil || generation != 1 {
		t.Fatal(generation, err)
	}
	empty, err := services[0].Create(ctx, owner, CreateInput{IdempotencyKey: "00000000-0000-0000-0000-000000000011", Name: "empty", SHA256: sha("")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services[1].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	empty, err = services[0].Get(ctx, owner, empty.ID)
	if err != nil || empty.Status != "available" {
		t.Fatal(empty, err)
	}
	for _, service := range services {
		reader, name, size, err := service.Download(ctx, owner, *empty.FileID)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil || len(body) != 0 || size != 0 || name != "empty" {
			t.Fatal("empty download", name, size, len(body), readErr)
		}
	}
	// A returning node must get fresh post-StartSync writes, even when old copies exist.
	target := services[2].cfg.LocalNode
	if _, err = pool.Exec(ctx, "UPDATE cluster_nodes SET state='unavailable' WHERE id=$1", target.ID); err != nil {
		t.Fatal(err)
	}
	authority := cluster.Lease{HolderID: services[0].cfg.LocalNode.ID, Term: 1}
	if err = services[0].cfg.Cluster.StartSync(ctx, authority, target.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := services[0].cfg.Cluster.AdmissionPlan(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services[0].cfg.Cluster.Admit(ctx, authority, target.ID, plan.PublicationGeneration); !errors.Is(err, cluster.ErrSyncRequired) {
		t.Fatal("admitted stale receipts", err)
	}
	if err = services[0].SyncPublished(ctx, plan.Node, plan); err != nil {
		t.Fatal(err)
	}
	if _, err = services[0].cfg.Cluster.Admit(ctx, authority, target.ID, plan.PublicationGeneration); err != nil {
		t.Fatal(err)
	}
}

func TestRealDeletedOperationIsHiddenFromUserAndAdmin(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000012", "deleted.bin")
	if found, err := services[0].processNext(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	published, err := services[0].Get(ctx, owner, op.ID)
	if err != nil || published.FileID == nil {
		t.Fatal(published, err)
	}
	if _, err = (catalog.Service{Pool: pool}).DeleteFile(ctx, owner, *published.FileID); err != nil {
		t.Fatal(err)
	}
	if _, err = services[0].Get(ctx, owner, op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted operation Get error = %v", err)
	}
	operations, err := services[0].List(ctx, owner)
	if err != nil || len(operations) != 0 {
		t.Fatalf("deleted operation list = %#v, error = %v", operations, err)
	}
	// Tombstones are normally created only for available operations. Temporarily
	// presenting this one as pending proves the admin projection applies its own
	// exclusion instead of relying on that current invariant.
	if _, err = pool.Exec(ctx, "UPDATE upload_operations SET status='pending',phase='receiving' WHERE id=$1", op.ID); err != nil {
		t.Fatal(err)
	}
	view, err := (admin.Service{Pool: pool}).View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Operations) != 0 {
		t.Fatalf("admin exposed tombstoned operation: %#v", view.Operations)
	}
}
func TestRealWorkerFencesAndIntegrity(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000020", "takeover.bin")
	old, found, err := services[0].claimNext(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE upload_operations SET lease_expires_at=clock_timestamp()-INTERVAL '1 second' WHERE id=$1", op.ID); err != nil {
		t.Fatal(err)
	}
	successor, found, err := services[1].claimNext(ctx)
	if err != nil || !found || successor.Generation <= old.Generation {
		t.Fatal(found, successor, err)
	}
	if err = services[0].fencedTx(ctx, old, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, errLeaseLost) {
		t.Fatal("old worker accepted", err)
	}
	if err = services[1].renew(ctx, successor); err != nil {
		t.Fatal(err)
	}
	if err = services[1].process(ctx, successor); err != nil {
		t.Fatal(err)
	}
	bad := inputFor("00000000-0000-0000-0000-000000000021")
	bad.Name = "wrong-full-hash"
	bad.SHA256 = sha("xxxxxx")
	mismatch, err := services[0].Create(ctx, owner, bad)
	if err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{"one", "two"} {
		if err = services[i].PutPart(ctx, owner, mismatch.ID, int64(i), bytes.NewBufferString(data)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = services[0].processNext(ctx); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatal("full hash accepted", err)
	}
	mismatch, err = services[0].Get(ctx, owner, mismatch.ID)
	if err != nil || mismatch.Status != "pending" || mismatch.ErrorCode == nil || *mismatch.ErrorCode != "content_mismatch" {
		t.Fatal(mismatch, err)
	}
	cancelled := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000022", "cancel-worker")
	active, found, err := services[0].claimNext(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err = services[1].Cancel(ctx, owner, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if err = services[0].process(ctx, active); !errors.Is(err, errLeaseLost) {
		t.Fatal("cancelled operation finalized", err)
	}
}
func TestRealMembershipChangesDuringCopy(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000030", "membership.bin")
	worker := *services[0]
	factory := worker.cfg.StorageFor
	removed := false
	worker.cfg.StorageFor = func(n cluster.Node) (*storage.Store, error) {
		if n.ID == services[2].cfg.LocalNode.ID && !removed {
			removed = true
			if _, err := pool.Exec(ctx, "DELETE FROM cluster_membership WHERE node_id=$1", n.ID); err != nil {
				return nil, err
			}
			if _, err := pool.Exec(ctx, "UPDATE cluster_nodes SET state='unavailable' WHERE id=$1", n.ID); err != nil {
				return nil, err
			}
			if _, err := pool.Exec(ctx, "UPDATE cluster_configuration SET version=version+1 WHERE singleton=true"); err != nil {
				return nil, err
			}
		}
		return factory(n)
	}
	if _, err := worker.processNext(ctx); err == nil {
		t.Fatal("old membership unexpectedly completed")
	}
	if _, err := services[0].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := services[0].Get(ctx, owner, op.ID)
	if err != nil || current.Status != "available" {
		t.Fatal(current, err)
	}
	// An admission before publication adds a required receipt; it cannot be skipped.
	op = createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000031", "admission.bin")
	added := false
	worker = *services[0]
	worker.cfg.StorageFor = func(n cluster.Node) (*storage.Store, error) {
		if n.ID == services[1].cfg.LocalNode.ID && !added {
			added = true
			if _, err := pool.Exec(ctx, "UPDATE cluster_nodes SET state='ready' WHERE id=$1", services[2].cfg.LocalNode.ID); err != nil {
				return nil, err
			}
			if _, err := pool.Exec(ctx, "INSERT INTO cluster_membership(node_id) VALUES($1)", services[2].cfg.LocalNode.ID); err != nil {
				return nil, err
			}
			if _, err := pool.Exec(ctx, "UPDATE cluster_configuration SET version=version+1 WHERE singleton=true"); err != nil {
				return nil, err
			}
		}
		return factory(n)
	}
	if _, err := worker.processNext(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("publication skipped newly required site", err)
	}
	if _, err := services[0].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	current, err = services[0].Get(ctx, owner, op.ID)
	if err != nil || current.Status != "available" {
		t.Fatal(current, err)
	}
}

func TestRealReplicaFallback(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000040", "replica-fallback.bin")
	var key, version string
	if err := pool.QueryRow(ctx, "SELECT object_key,s3_version_id FROM upload_part_copies WHERE operation_id=$1 AND part_index=0", op.ID).Scan(&key, &version); err != nil {
		t.Fatal(err)
	}
	// Wait for a physical version listing on site 2 before making the source
	// endpoint unavailable through the service factory. This is not a new receipt.
	core, err := minio.NewCore(services[1].cfg.LocalNode.StorageEndpoint[len("http://"):], &minio.Options{Creds: credentials.NewStaticV4("minioadmin", "minioadmin", ""), Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	replicated := false
	for attempt := 0; attempt < 50 && !replicated; attempt++ {
		for object := range core.Client.ListObjects(ctx, "drive-clone", minio.ListObjectsOptions{Prefix: key, WithVersions: true}) {
			if object.Err != nil {
				t.Fatal(object.Err)
			}
			if object.Key == key && object.VersionID == version {
				replicated = true
			}
		}
		if !replicated {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !replicated {
		t.Fatal("replicated part did not become locally listed")
	}
	if err = database.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM cluster_membership WHERE node_id=$1", services[0].cfg.LocalNode.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE cluster_nodes SET state='unavailable',storage_generation=gen_random_uuid() WHERE id=$1", services[0].cfg.LocalNode.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	worker := *services[2]
	original := worker.cfg.StorageFor
	worker.cfg.StorageFor = func(n cluster.Node) (*storage.Store, error) {
		if n.ID == services[0].cfg.LocalNode.ID {
			return nil, ErrUnavailable
		}
		return original(n)
	}
	found, err := worker.Get(ctx, owner, op.ID)
	if err != nil || !found.Parts[0].Available {
		t.Fatal("replicated part was ignored", found, err)
	}
	if _, err = worker.processNext(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM upload_part_copies WHERE operation_id=$1 AND part_index=0", op.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("fallback read generated a receipt", count, err)
	}
}

func TestRealRenewalDuringBlockedCopy(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000050", "renewal.bin")
	worker := *services[0]
	factory := worker.cfg.StorageFor
	entered := make(chan struct{})
	release := make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	worker.cfg.StorageFor = func(n cluster.Node) (*storage.Store, error) {
		once.Do(func() { close(entered); <-release })
		return factory(n)
	}
	finished := make(chan error, 1)
	go func() { _, err := worker.processNext(ctx); finished <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	var initial time.Time
	if err := pool.QueryRow(ctx, "SELECT lease_expires_at FROM upload_operations WHERE id=$1", op.ID).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	renewed := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var expiry time.Time
		if err := pool.QueryRow(ctx, "SELECT lease_expires_at FROM upload_operations WHERE id=$1", op.ID).Scan(&expiry); err != nil {
			t.Fatal(err)
		}
		if expiry.After(initial) {
			renewed = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if !renewed {
		t.Fatal("lease did not renew while copy was blocked")
	}
}

func TestRealMetadataDeadline(t *testing.T) {
	ctx, pool, services, _, _ := uploadFixture(t)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	// Keep this fixture lock beyond the client deadline; server idle expiry
	// is covered separately by the abandoned-transaction database test.
	if _, err := blocker.Exec(ctx, "SET LOCAL idle_in_transaction_session_timeout = '20s'"); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, "SELECT version FROM cluster_configuration WHERE singleton=true FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = services[0].userTx(ctx, func(ctx context.Context, tx pgx.Tx) error { return nil })
	var sqlError *pgconn.PgError
	serverDeadline := errors.As(err, &sqlError) && sqlError.Code == "57014"
	if !errors.Is(err, context.DeadlineExceeded) && !serverDeadline {
		t.Fatal("metadata wait was not bounded", err)
	}
	t.Logf("metadata wait rejected after %s: %v", time.Since(started), err)
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatal("metadata timeout exceeded", elapsed)
	}
}

func TestRealLostPartCanBeResent(t *testing.T) {
	ctx, pool, services, owner, _ := uploadFixture(t)
	op := createReceived(t, ctx, services, owner, "00000000-0000-0000-0000-000000000090", "lost-part.bin")
	var key, version string
	if err := pool.QueryRow(ctx, "SELECT object_key,s3_version_id FROM upload_part_copies WHERE operation_id=$1 AND part_index=0", op.ID).Scan(&key, &version); err != nil {
		t.Fatal(err)
	}
	// Remove only this test operation's exact version on every site. Leaving the
	// SQL receipt tests that historical metadata is not mistaken for usable bytes.
	for _, service := range services {
		if err := service.cfg.LocalStorage.RemovePartVersion(ctx, op.ID, key, version); err != nil {
			t.Fatal(err)
		}
	}
	current, err := services[2].Get(ctx, owner, op.ID)
	if err != nil || current.Parts[0].Availability != "missing" || !current.Parts[1].Available {
		t.Fatal("lost and preserved parts were not distinguished", current, err)
	}
	if err := services[1].PutPart(ctx, owner, op.ID, 0, bytes.NewBufferString("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := services[2].processNext(ctx); err != nil {
		t.Fatal(err)
	}
	current, err = services[0].Get(ctx, owner, op.ID)
	if err != nil || current.Status != "available" {
		t.Fatal("resent part failed to restore publication", current, err)
	}
}
