package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sha(data string) string { sum := sha256.Sum256([]byte(data)); return hex.EncodeToString(sum[:]) }
func inputFor(key string) CreateInput {
	return CreateInput{IdempotencyKey: key, Name: "example.bin", Size: 6, SHA256: sha("onetwo"), Parts: []PartInput{{0, 0, 3, sha("one")}, {1, 3, 3, sha("two")}}}
}
func TestManifestValidation(t *testing.T) {
	base := inputFor("00000000-0000-0000-0000-000000000001")
	if _, err := normalize(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CreateInput){
		func(i *CreateInput) { i.Size = -1 }, func(i *CreateInput) { i.Size = maxSafeInteger + 1 }, func(i *CreateInput) { i.Parts[1].Offset = 0 }, func(i *CreateInput) { i.Parts[1].Index = 0 }, func(i *CreateInput) { i.Parts[0].Size = 0 }, func(i *CreateInput) { i.Parts[0].SHA256 = "bad" }, func(i *CreateInput) { i.SHA256 = "bad" }, func(i *CreateInput) { i.IdempotencyKey = "bad" }, func(i *CreateInput) { i.Size = 7 },
	} {
		candidate := base
		candidate.Parts = append([]PartInput{}, base.Parts...)
		mutate(&candidate)
		if _, err := normalize(candidate); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid manifest accepted", candidate, err)
		}
	}
	large := base
	large.Size = 3 << 30
	large.Parts = nil
	for offset := int64(0); offset < large.Size; offset += MaxPartSize {
		large.Parts = append(large.Parts, PartInput{Index: int64(len(large.Parts)), Offset: offset, Size: min(MaxPartSize, large.Size-offset), SHA256: sha("part identity")})
	}
	if _, err := normalize(large); err != nil {
		t.Fatal("reference file size became a limit", err)
	}
	empty := CreateInput{IdempotencyKey: base.IdempotencyKey, Name: "empty", SHA256: sha("")}
	if _, err := normalize(empty); err != nil {
		t.Fatal(err)
	}
}
func uploadFixture(t *testing.T) (context.Context, *pgxpool.Pool, []*Service, string, string) {
	t.Helper()
	raw := os.Getenv("ACERVO_UPLOADS_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set ACERVO_UPLOADS_TEST_DATABASE_URL for real SQL/S3 tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	admin, err := database.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("uploads_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pool, err := database.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cs := cluster.New(pool)
	services := []*Service{}
	for n := 1; n <= 3; n++ {
		node, err := cs.Register(ctx, cluster.Registration{NodeID: fmt.Sprintf("node-%d", n), BackendEndpoint: fmt.Sprintf("http://localhost:%d", 27800+n), DatabaseEndpoint: "postgresql://localhost:27657/drive_clone", StorageEndpoint: fmt.Sprintf("http://127.0.0.1:%d", 27900+n)})
		if err != nil {
			t.Fatal(err)
		}
		factory := func(node cluster.Node) (*storage.Store, error) {
			return storage.New(storage.Options{Endpoint: node.StorageEndpoint, Bucket: "drive-clone", AccessKey: "minioadmin", SecretKey: "minioadmin"})
		}
		local, err := factory(node)
		if err != nil {
			t.Fatal(err)
		}
		svc, err := New(ServiceConfig{Pool: pool, LocalNode: node, LocalStorage: local, Cluster: cs, StorageFor: factory, Eligible: func(context.Context) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, svc)
		if _, err = pool.Exec(ctx, "UPDATE cluster_nodes SET state='ready' WHERE id=$1;", node.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, "INSERT INTO cluster_membership(node_id) VALUES($1)", node.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, "UPDATE manager_lease SET holder_id=$1,term=1,expires_at=clock_timestamp()+INTERVAL '10 minutes'", services[0].cfg.LocalNode.ID); err != nil {
		t.Fatal(err)
	}
	var owner, other string
	if err = pool.QueryRow(ctx, "INSERT INTO users(login,password_hash) VALUES('owner','fixture') RETURNING id::STRING").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "INSERT INTO users(login,password_hash) VALUES('other','fixture') RETURNING id::STRING").Scan(&other); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, services, owner, other
}
func TestRealOperationLifecycle(t *testing.T) {
	ctx, pool, services, owner, other := uploadFixture(t)
	input := inputFor("00000000-0000-0000-0000-000000000001")
	var wg sync.WaitGroup
	ops := make(chan Operation, 2)
	errs := make(chan error, 2)
	for _, svc := range services[:2] {
		wg.Add(1)
		go func(s *Service) { defer wg.Done(); op, err := s.Create(ctx, owner, input); ops <- op; errs <- err }(svc)
	}
	wg.Wait()
	close(ops)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var op Operation
	for current := range ops {
		if op.ID != "" && op.ID != current.ID {
			t.Fatal("duplicated operation")
		}
		op = current
	}
	bad := input
	bad.Name = "different.bin"
	if _, err := services[0].Create(ctx, owner, bad); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency content conflict", err)
	}
	if _, err := services[0].Get(ctx, other, op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("private operation exposed", err)
	}
	if list, err := services[0].List(ctx, other); err != nil || len(list) != 0 {
		t.Fatal("foreign operation listed", list, err)
	}
	if err := services[0].PutPart(ctx, other, op.ID, 0, bytes.NewBufferString("one")); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign upload accepted", err)
	}
	if err := services[0].PutPart(ctx, owner, op.ID, 0, bytes.NewBufferString("bad")); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatal("invalid bytes accepted", err)
	}
	for n, data := range []string{"one", "two"} {
		if err := services[n].PutPart(ctx, owner, op.ID, int64(n), bytes.NewBufferString(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := services[0].PutPart(ctx, owner, op.ID, 0, bytes.NewBufferString("one")); err != nil {
		t.Fatal(err)
	}
	got, err := services[2].Get(ctx, owner, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Parts {
		if !p.Available {
			t.Fatal("preserved part unavailable", p)
		}
	}
	broken := *services[2]
	broken.cfg.StorageFor = func(cluster.Node) (*storage.Store, error) { return nil, errors.New("unreachable") }
	got, err = broken.Get(ctx, owner, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parts[0].Availability != "unknown" {
		t.Fatal("network failure became missing")
	}
	if _, err = pool.Exec(ctx, "UPDATE cluster_nodes SET storage_generation=gen_random_uuid() WHERE id=$1", services[0].cfg.LocalNode.ID); err != nil {
		t.Fatal(err)
	}
	got, err = broken.Get(ctx, owner, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parts[0].Availability != "unknown" {
		t.Fatal("historical version lost during origin replacement", got.Parts)
	}
	sources, err := services[2].partSources(ctx, op.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Node.ID == services[0].cfg.LocalNode.ID {
			t.Fatal("replaced origin retained as a byte source")
		}
	}

	if err := services[0].PutPart(ctx, owner, op.ID, 0, bytes.NewBufferString("one")); !errors.Is(err, ErrUnavailable) {
		t.Fatal("stale storage generation accepted", err)
	}
	if _, err := services[0].Cancel(ctx, other, op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign cancel allowed", err)
	}
	for range 2 {
		cancelled, err := services[1].Cancel(ctx, owner, op.ID)
		if err != nil || cancelled.Status != "cancelled" {
			t.Fatal(cancelled, err)
		}
	}
	if err := services[1].PutPart(ctx, owner, op.ID, 1, bytes.NewBufferString("two")); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled upload accepted part", err)
	}
	list, err := services[2].List(ctx, owner)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM cluster_membership WHERE node_id=$1", services[2].cfg.LocalNode.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = services[2].Get(ctx, owner, op.ID); !errors.Is(err, cluster.ErrNoAuthority) {
		t.Fatal("excluded node served operation", err)
	}
}
