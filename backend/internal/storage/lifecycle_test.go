package storage

import (
	"context"
	"errors"
	"testing"
)

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
