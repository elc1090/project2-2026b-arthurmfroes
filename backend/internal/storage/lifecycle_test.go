package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

type removeCall struct {
	key, version string
}

type removeSpy struct {
	objectAPI
	versions map[string]map[string]bool
	calls    []removeCall
	failOnce error
}

func (s *removeSpy) RemoveObject(_ context.Context, _ string, key string, options minio.RemoveObjectOptions) error {
	s.calls = append(s.calls, removeCall{key: key, version: options.VersionID})
	versions := s.versions[key]
	if !versions[options.VersionID] {
		return minio.ErrorResponse{Code: minio.NoSuchVersion}
	}
	delete(versions, options.VersionID)
	if s.failOnce != nil {
		err := s.failOnce
		s.failOnce = nil
		return err
	}
	return nil
}

func TestLifecycleValidationAndGate(t *testing.T) {
	denied := errors.New("injected storage fault")
	s := &Store{before: func(context.Context) error { return denied }}
	id := "00000000-0000-0000-0000-000000000001"
	if err := s.VisitPartVersions(context.Background(), id, func(string, string) error { return nil }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if err := s.RemovePartVersion(context.Background(), id, "parts/"+id+"/0/hash", "version"); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if err := s.RemoveFinalVersion(context.Background(), "objects/"+id+"/"+strings.Repeat("a", 64), "version"); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "../", "00000000-0000-0000-0000-00000000000A"} {
		if err := s.VisitPartVersions(context.Background(), invalid, func(string, string) error { return nil }); !errors.Is(err, ErrInvalid) {
			t.Fatal(invalid, err)
		}
	}
	for _, key := range []string{"objects/" + id + "/hash", "parts/" + id + "suffix/hash"} {
		if err := s.RemovePartVersion(context.Background(), id, key, "version"); !errors.Is(err, ErrInvalid) {
			t.Fatal(key, err)
		}
	}
	for _, version := range []string{"", "null"} {
		if err := s.RemovePartVersion(context.Background(), id, "parts/"+id+"/0/hash", version); !errors.Is(err, ErrInvalid) {
			t.Fatal(version, err)
		}
	}
}

func TestRemoveFinalVersionDeletesOnlyExactVersion(t *testing.T) {
	id := "00000000-0000-0000-0000-000000000001"
	key := "objects/" + id + "/" + strings.Repeat("a", 64)
	otherKey := "objects/" + id + "/" + strings.Repeat("b", 64)
	spy := &removeSpy{versions: map[string]map[string]bool{
		key:      {"version-1": true, "version-2": true},
		otherKey: {"version-1": true},
	}}
	store := &Store{api: spy, bucket: "test-bucket"}
	if err := store.RemoveFinalVersion(context.Background(), key, "version-1"); err != nil {
		t.Fatal(err)
	}
	if spy.versions[key]["version-1"] || !spy.versions[key]["version-2"] || !spy.versions[otherKey]["version-1"] {
		t.Fatalf("unexpected remaining versions: %#v", spy.versions)
	}
	if len(spy.calls) != 1 || spy.calls[0] != (removeCall{key: key, version: "version-1"}) {
		t.Fatalf("calls=%+v", spy.calls)
	}
	if err := store.RemoveFinalVersion(context.Background(), key, "version-1"); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
}

func TestRemoveFinalVersionKeepsReceiptAfterAmbiguousFailure(t *testing.T) {
	id := "00000000-0000-0000-0000-000000000001"
	key := "objects/" + id + "/" + strings.Repeat("c", 64)
	ambiguous := errors.New("connection closed before response")
	spy := &removeSpy{
		versions: map[string]map[string]bool{key: {"physical-version": true}},
		failOnce: ambiguous,
	}
	store := &Store{api: spy, bucket: "test-bucket"}
	if err := store.RemoveFinalVersion(context.Background(), key, "physical-version"); !errors.Is(err, ambiguous) {
		t.Fatalf("ambiguous result lost: %v", err)
	}
	if err := store.RemoveFinalVersion(context.Background(), key, "physical-version"); err != nil {
		t.Fatalf("retry did not reconcile absence: %v", err)
	}
	if len(spy.calls) != 2 || spy.calls[0] != spy.calls[1] {
		t.Fatalf("retry changed physical target: %+v", spy.calls)
	}
}

func TestRemoveFinalVersionRejectsNonFinalTargetsWithoutIO(t *testing.T) {
	id := "a0000000-0000-0000-0000-000000000001"
	hash := strings.Repeat("d", 64)
	valid := "objects/" + id + "/" + hash
	spy := &removeSpy{versions: map[string]map[string]bool{}}
	store := &Store{api: spy, bucket: "test-bucket"}
	for _, test := range []struct{ key, version string }{
		{"parts/" + id + "/0/" + hash, "version"},
		{"_health/" + id + "/" + hash, "version"},
		{"objects/" + id + "/" + hash + "/suffix", "version"},
		{"objects/" + strings.ToUpper(id) + "/" + hash, "version"},
		{"objects/" + id + "/" + strings.ToUpper(hash), "version"},
		{"objects/" + id + "/short", "version"},
		{valid, ""},
		{valid, "null"},
	} {
		if err := store.RemoveFinalVersion(context.Background(), test.key, test.version); !errors.Is(err, ErrInvalid) {
			t.Fatal(fmt.Sprintf("key=%q version=%q err=%v", test.key, test.version, err))
		}
	}
	if len(spy.calls) != 0 {
		t.Fatalf("invalid target reached storage: %+v", spy.calls)
	}
}
