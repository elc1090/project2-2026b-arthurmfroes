package cluster

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCockroachAdmission(t *testing.T) {
	pool, ctx := integrationPool(t)
	store := New(pool)
	manager, err := store.Register(ctx, registration("manager"))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Register(ctx, registration("candidate"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireLease(ctx, manager.ID, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// A deliberately inconsistent fixture confirms StartSync removes membership.
	if _, err := pool.Exec(ctx, "INSERT INTO cluster_membership(node_id) VALUES($1)", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartSync(ctx, lease, candidate.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Snapshot(ctx)
	if err != nil || snap.Version != 1 || len(snap.Members) != 0 {
		t.Fatalf("sync snapshot=%v err=%v", snap, err)
	}
	if err := store.StartSync(ctx, lease, candidate.ID); err != nil {
		t.Fatal(err)
	}
	first := publishFixture(t, ctx, pool, "first")
	plan, err := store.AdmissionPlan(ctx, candidate.ID)
	if err != nil || plan.PublicationGeneration != 1 || len(plan.Files) != 1 || plan.Files[0].FreshReceipt {
		t.Fatalf("missing plan=%v err=%v", plan, err)
	}
	if _, err := store.Admit(ctx, lease, candidate.ID, 1); !errors.Is(err, ErrSyncRequired) {
		t.Fatalf("admitted missing bytes: %v", err)
	}
	receiptFixture(t, ctx, pool, first, candidate.ID, true)
	if _, err := store.Admit(ctx, lease, candidate.ID, 1); !errors.Is(err, ErrSyncRequired) {
		t.Fatalf("admitted old storage generation: %v", err)
	}
	receiptFixture(t, ctx, pool, first, candidate.ID, false)
	if _, err := pool.Exec(ctx, "UPDATE object_copies SET verified_at='2000-01-01' WHERE operation_id=$1", first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Admit(ctx, lease, candidate.ID, 1); !errors.Is(err, ErrSyncRequired) {
		t.Fatalf("admitted stale verification: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE object_copies SET verified_at=clock_timestamp() WHERE operation_id=$1", first); err != nil {
		t.Fatal(err)
	}
	plan, err = store.AdmissionPlan(ctx, candidate.ID)
	if err != nil || !plan.Files[0].FreshReceipt {
		t.Fatalf("fresh plan=%v err=%v", plan, err)
	}

	// The writer owns the exact row the admission barrier needs. It publishes a
	// new file after planning but before admission can commit.
	writer, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	var generation int64
	if err := writer.QueryRow(ctx, "SELECT publication_generation FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&generation); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := store.Admit(ctx, lease, candidate.ID, plan.PublicationGeneration); result <- err }()
	second, err := insertPublishedFixture(ctx, writer, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrSyncRequired) {
		t.Fatalf("concurrent publication not noticed: %v", err)
	}
	snap, err = store.Snapshot(ctx)
	if err != nil || len(snap.Members) != 0 || snap.PublicationGeneration != 2 {
		t.Fatalf("premature member: %v err=%v", snap, err)
	}
	if _, err := store.Admit(ctx, lease, candidate.ID, 2); !errors.Is(err, ErrSyncRequired) {
		t.Fatalf("new missing file ignored: %v", err)
	}
	receiptFixture(t, ctx, pool, second, candidate.ID, false)
	admitted, err := store.Admit(ctx, lease, candidate.ID, 2)
	if err != nil || admitted != 2 {
		t.Fatalf("admission version=%d err=%v", admitted, err)
	}
	snap, err = store.Snapshot(ctx)
	if err != nil || len(snap.Members) != 1 || snap.Members[0].State != "ready" || snap.PublicationGeneration != 2 {
		t.Fatalf("final snapshot=%v err=%v", snap, err)
	}
	t.Log("missing/stale receipts rejected; concurrent publication forced generation 1 -> 2; admitted only after both receipts")

	expiredNode, err := store.Register(ctx, registration("expired-candidate"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartSync(ctx, lease, expiredNode.ID); err != nil {
		t.Fatal(err)
	}
	short, err := store.RenewLease(ctx, lease, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for {
		var expired bool
		if err := pool.QueryRow(ctx, "SELECT clock_timestamp()>$1::TIMESTAMPTZ", short.ExpiresAt).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := store.Admit(ctx, short, expiredNode.ID, 2); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("expired authority admitted: %v", err)
	}
	if err := store.StartSync(ctx, short, expiredNode.ID); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("expired authority started sync: %v", err)
	}
}

func publishFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	err := database.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var generation int64
		if err := tx.QueryRow(ctx, "SELECT publication_generation FROM cluster_configuration WHERE singleton=true FOR UPDATE").Scan(&generation); err != nil {
			return err
		}
		var err error
		id, err = insertPublishedFixture(ctx, tx, name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Fixtures stand in for the later publication/MinIO implementation; tests here
// verify SQL coordination and never claim that these receipts prove real bytes.
func insertPublishedFixture(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var owner, id string
	if err := tx.QueryRow(ctx, "INSERT INTO users(login,password_hash) VALUES($1,'test-hash') RETURNING id::STRING", name).Scan(&owner); err != nil {
		return "", err
	}
	checksum := sha256.Sum256(nil)
	if err := tx.QueryRow(ctx, `INSERT INTO upload_operations(owner_id,name,idempotency_key,size_bytes,sha256,manifest,part_count,status,phase) VALUES($1,$2,gen_random_uuid(),0,$3,'[]',0,'available','complete') RETURNING id::STRING`, owner, name, checksum[:]).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO files(operation_id,owner_id,name,configuration_version) SELECT $1,$2,$3,version FROM cluster_configuration WHERE singleton=true", id, owner, name); err != nil {
		return "", err
	}
	_, err := tx.Exec(ctx, "UPDATE cluster_configuration SET publication_generation=publication_generation+1 WHERE singleton=true")
	return id, err
}
func receiptFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, operation, node string, oldGeneration bool) {
	t.Helper()
	generation := "n.storage_generation"
	if oldGeneration {
		generation = "gen_random_uuid()"
	}
	_, err := pool.Exec(ctx, `INSERT INTO object_copies(operation_id,node_id,storage_generation,object_key,s3_version_id,size_bytes,sha256,verified_at)
 SELECT u.id,n.id,`+generation+`,'object','version',u.size_bytes,u.sha256,clock_timestamp() FROM upload_operations u CROSS JOIN cluster_nodes n WHERE u.id=$1 AND n.id=$2`, operation, node)
	if err != nil {
		t.Fatal(err)
	}
}
