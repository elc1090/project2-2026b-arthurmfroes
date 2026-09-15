package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
)

type versionLister interface {
	ListObjects(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
}

// VisitPartVersions enumerates physical temporary versions in one operation's
// exact namespace. It never includes published object keys or delete markers.
// The callback runs serially, keeping listing memory independent of file size.
func (s *Store) VisitPartVersions(ctx context.Context, operation string, visit func(key, version string) error) error {
	if !canonicalOperation(operation) || visit == nil {
		return ErrInvalid
	}
	if s.before != nil {
		if err := s.before(ctx); err != nil {
			return err
		}
	}
	api, ok := s.api.(versionLister)
	// Core has its own low-level ListObjects method, hiding Client.ListObjects.
	if core, isCore := s.api.(*minio.Core); isCore {
		api, ok = core.Client, true
	}
	if !ok {
		return fmt.Errorf("storage does not support version listing")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	prefix := "parts/" + operation + "/"
	for object := range api.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true, WithVersions: true}) {
		if object.Err != nil {
			return object.Err
		}
		if object.IsDeleteMarker {
			continue
		}
		if !strings.HasPrefix(object.Key, prefix) || object.VersionID == "" || object.VersionID == "null" {
			return ErrInvalid
		}
		if err := visit(object.Key, object.VersionID); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// RemovePartVersion requires an exact version and operation prefix. Callers
// must first establish that the operation is terminal and has no live reference.
func (s *Store) RemovePartVersion(ctx context.Context, operation, key, version string) error {
	if !canonicalOperation(operation) || !strings.HasPrefix(key, "parts/"+operation+"/") || version == "" || version == "null" {
		return ErrInvalid
	}
	if s.before != nil {
		if err := s.before(ctx); err != nil {
			return err
		}
	}
	return s.api.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{VersionID: version})
}

func canonicalOperation(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
