// Package storage writes verified, versioned objects to one explicitly configured
// local MinIO endpoint. SQL publication, node eligibility and storage generations
// belong to the caller; a receipt alone does not publish a file.
package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

const (
	DefaultPartSize int64 = 32 << 20
	minPartSize     int64 = 5 << 20
	maxPartSize     int64 = 5 << 30
	maxParts        int64 = 10000
)

var (
	ErrInvalid     = errors.New("invalid storage argument")
	ErrIntegrity   = errors.New("object content does not match its declared size or SHA-256")
	ErrUnversioned = errors.New("storage did not return a physical object version")
)

type Options struct {
	// BeforeOperation can reject actual I/O during an injected development failure.
	BeforeOperation func(context.Context) error
	Endpoint        string
	AccessKey       string
	SecretKey       string
	Bucket          string
	// PartSize is the preferred multipart size. Large objects automatically use
	// larger streamed parts to stay within the protocol's 10,000-part limit.
	PartSize int64
}

type Receipt struct {
	Key       string
	VersionID string
	Size      int64
	SHA256    [sha256.Size]byte
}

type ObjectInfo struct {
	Size        int64
	VersionID   string
	ContentType string
}

type objectAPI interface {
	NewMultipartUpload(context.Context, string, string, minio.PutObjectOptions) (string, error)
	PutObjectPart(context.Context, string, string, string, int, io.Reader, int64, minio.PutObjectPartOptions) (minio.ObjectPart, error)
	CompleteMultipartUpload(context.Context, string, string, string, []minio.CompletePart, minio.PutObjectOptions) (minio.UploadInfo, error)
	AbortMultipartUpload(context.Context, string, string, string) error
	PutObject(context.Context, string, string, io.Reader, int64, string, string, minio.PutObjectOptions) (minio.UploadInfo, error)
	GetObject(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error)
	RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
}

type Store struct {
	before   func(context.Context) error
	api      objectAPI
	bucket   string
	partSize int64
}

func New(opts Options) (*Store, error) {
	endpoint, err := url.Parse(opts.Endpoint)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, fmt.Errorf("%w: endpoint must be an HTTP(S) origin", ErrInvalid)
	}
	if opts.AccessKey == "" || opts.SecretKey == "" {
		return nil, fmt.Errorf("%w: storage credentials required", ErrInvalid)
	}
	if err := s3utils.CheckValidBucketName(opts.Bucket); err != nil {
		return nil, fmt.Errorf("%w: invalid bucket", ErrInvalid)
	}
	if opts.PartSize == 0 {
		opts.PartSize = DefaultPartSize
	}
	if opts.PartSize < minPartSize || opts.PartSize > maxPartSize {
		return nil, fmt.Errorf("%w: part size outside multipart limits", ErrInvalid)
	}
	core, err := minio.NewCore(endpoint.Host, &minio.Options{
		Creds: credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, ""), Secure: endpoint.Scheme == "https",
		Region: "us-east-1", BucketLookup: minio.BucketLookupPath, MaxRetries: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize local storage: %w", err)
	}
	return &Store{before: opts.BeforeOperation, api: core, bucket: opts.Bucket, partSize: opts.PartSize}, nil
}

// Write derives a content-addressed key from a trusted operation/part prefix.
// Repetition can create another physical version of identical bytes. A different
// checksum produces a different key and cannot overwrite the original content.
// Nonempty objects use low-level multipart even when they fit in one part, so
// neither incomplete nor corrupt content is committed before final validation.
// The caller owns r and must unblock its Read when cancelling a blocking source.
func (s *Store) Write(ctx context.Context, prefix string, size int64, sum [sha256.Size]byte, r io.Reader) (receipt Receipt, err error) {
	if s.before != nil {
		if err := s.before(ctx); err != nil {
			return Receipt{}, err
		}
	}
	if size < 0 || r == nil || prefix == "" || strings.HasSuffix(prefix, "/") {
		return Receipt{}, ErrInvalid
	}
	key := prefix + "/" + hex.EncodeToString(sum[:])
	if check := s3utils.CheckValidObjectName(key); check != nil {
		return Receipt{}, fmt.Errorf("%w: invalid object key", ErrInvalid)
	}
	partSize, err := multipartSize(size, s.partSize)
	if err != nil {
		return Receipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return Receipt{}, err
	}
	options := minio.PutObjectOptions{ContentType: "application/octet-stream"}
	var info minio.UploadInfo
	if size == 0 {
		if err = expectEOF(ctx, r); err != nil {
			return Receipt{}, err
		}
		if sum != sha256.Sum256(nil) {
			return Receipt{}, ErrIntegrity
		}
		info, err = s.api.PutObject(ctx, s.bucket, key, bytes.NewReader(nil), 0, "", hex.EncodeToString(sum[:]), options)
	} else {
		uploadID, createErr := s.api.NewMultipartUpload(ctx, s.bucket, key, options)
		if createErr != nil {
			return Receipt{}, fmt.Errorf("start multipart: %w", createErr)
		}
		committed := false
		defer func() {
			if !committed {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if abortErr := s.api.AbortMultipartUpload(cleanup, s.bucket, key, uploadID); abortErr != nil {
					err = errors.Join(err, fmt.Errorf("abort multipart: %w", abortErr))
				}
			}
		}()
		hash := sha256.New()
		remaining := size
		parts := make([]minio.CompletePart, 0, (size-1)/partSize+1)
		for remaining > 0 {
			amount := min(remaining, partSize)
			// Core.PutObjectPart streams this non-seekable reader once. It does not
			// allocate a part-sized buffer or replay bytes after a transport failure.
			limited := &io.LimitedReader{R: contextReader{ctx: ctx, r: r}, N: amount}
			part, partErr := s.api.PutObjectPart(ctx, s.bucket, key, uploadID, len(parts)+1, io.TeeReader(limited, hash), amount, minio.PutObjectPartOptions{})
			if partErr != nil {
				return Receipt{}, fmt.Errorf("write multipart part: %w", partErr)
			}
			if limited.N != 0 {
				return Receipt{}, ErrIntegrity
			}
			parts = append(parts, minio.CompletePart{PartNumber: len(parts) + 1, ETag: part.ETag})
			remaining -= amount
		}
		if err = expectEOF(ctx, r); err != nil {
			return Receipt{}, err
		}
		if !bytes.Equal(hash.Sum(nil), sum[:]) {
			return Receipt{}, ErrIntegrity
		}
		if err = ctx.Err(); err != nil {
			return Receipt{}, err
		}
		info, err = s.api.CompleteMultipartUpload(ctx, s.bucket, key, uploadID, parts, options)
		if err != nil {
			return Receipt{}, fmt.Errorf("complete multipart: %w", err)
		}
		committed = true
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("write local object: %w", err)
	}
	if info.VersionID == "" || info.VersionID == "null" {
		return Receipt{}, ErrUnversioned
	}
	return Receipt{Key: key, VersionID: info.VersionID, Size: size, SHA256: sum}, nil
}

func multipartSize(size, preferred int64) (int64, error) {
	if size < 0 || preferred < minPartSize || preferred > maxPartSize || size > maxPartSize*maxParts {
		return 0, ErrInvalid
	}
	required := size / maxParts
	if size%maxParts != 0 {
		required++
	}
	return max(preferred, required), nil
}

func expectEOF(ctx context.Context, r io.Reader) error {
	var extra [1]byte
	n, err := io.ReadFull(contextReader{ctx: ctx, r: r}, extra[:])
	if n > 0 {
		return ErrIntegrity
	}
	if err == io.EOF {
		return nil
	}
	return err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Open reads exactly the requested physical version. A GET may be proxied by
// MinIO: its success is useful as a byte source, never as a local-copy receipt.
// The caller closes the reader and verifies content when using it for recovery.
func (s *Store) Open(ctx context.Context, key, version string) (io.ReadCloser, ObjectInfo, error) {
	if s.before != nil {
		if err := s.before(ctx); err != nil {
			return nil, ObjectInfo{}, err
		}
	}
	if version == "" || version == "null" {
		return nil, ObjectInfo{}, ErrInvalid
	}
	if err := s3utils.CheckValidObjectName(key); err != nil {
		return nil, ObjectInfo{}, ErrInvalid
	}
	reader, info, _, err := s.api.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{VersionID: version})
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("read object version: %w", err)
	}
	if info.VersionID != version {
		_ = reader.Close()
		return nil, ObjectInfo{}, ErrUnversioned
	}
	return reader, ObjectInfo{Size: info.Size, VersionID: info.VersionID, ContentType: info.ContentType}, nil
}

// Check proves an explicit local write and verifies its bytes. It removes only
// the newly created health object's physical version, never application files.
func (s *Store) Check(ctx context.Context) (err error) {
	var payload [32]byte
	if _, err = rand.Read(payload[:]); err != nil {
		return err
	}
	receipt, err := s.Write(ctx, "_health/"+hex.EncodeToString(payload[:16]), int64(len(payload)), sha256.Sum256(payload[:]), bytes.NewReader(payload[:]))
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if cleanupErr := s.api.RemoveObject(cleanup, s.bucket, receipt.Key, minio.RemoveObjectOptions{VersionID: receipt.VersionID}); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove health object version: %w", cleanupErr))
		}
	}()
	reader, _, err := s.Open(ctx, receipt.Key, receipt.VersionID)
	if err != nil {
		return err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, int64(len(payload))+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(body, payload[:]) {
		return ErrIntegrity
	}
	return nil
}
