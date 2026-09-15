package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/minio/minio-go/v7"
)

type writeSpy struct {
	objectAPI
	parts, completes, aborts, puts       int
	version                              string
	abortContextError                    error
	partError, completeError, abortError error
	afterPart                            func()
}

func (s *writeSpy) NewMultipartUpload(context.Context, string, string, minio.PutObjectOptions) (string, error) {
	return "upload", nil
}
func (s *writeSpy) PutObjectPart(_ context.Context, _, _, _ string, n int, r io.Reader, size int64, _ minio.PutObjectPartOptions) (minio.ObjectPart, error) {
	s.parts++
	if s.partError != nil {
		return minio.ObjectPart{}, s.partError
	}
	_, err := io.Copy(io.Discard, r)
	if s.afterPart != nil {
		s.afterPart()
	}
	return minio.ObjectPart{PartNumber: n, ETag: "etag", Size: size}, err
}
func (s *writeSpy) CompleteMultipartUpload(context.Context, string, string, string, []minio.CompletePart, minio.PutObjectOptions) (minio.UploadInfo, error) {
	s.completes++
	return minio.UploadInfo{VersionID: s.version}, s.completeError
}
func (s *writeSpy) AbortMultipartUpload(ctx context.Context, _, _, _ string) error {
	s.aborts++
	s.abortContextError = ctx.Err()
	return s.abortError
}
func (s *writeSpy) PutObject(context.Context, string, string, io.Reader, int64, string, string, minio.PutObjectOptions) (minio.UploadInfo, error) {
	s.puts++
	return minio.UploadInfo{VersionID: s.version}, nil
}

func TestInvalidContentNeverCompletes(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		size int64
		sum  [32]byte
	}{
		{"wrong hash", "abc", 3, sha256.Sum256([]byte("xyz"))},
		{"extra byte", "abcd", 3, sha256.Sum256([]byte("abc"))},
		{"short source", "ab", 3, sha256.Sum256([]byte("abc"))},
		{"nonempty zero", "x", 0, sha256.Sum256(nil)},
		{"empty hash mismatch", "", 0, sha256.Sum256([]byte("x"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &writeSpy{version: "v1"}
			s := Store{api: spy, bucket: "test-bucket", partSize: minPartSize}
			got, err := s.Write(context.Background(), "test", tc.size, tc.sum, bytes.NewBufferString(tc.data))
			if !errors.Is(err, ErrIntegrity) || got != (Receipt{}) {
				t.Fatalf("receipt=%+v err=%v", got, err)
			}
			if spy.completes != 0 || spy.puts != 0 {
				t.Fatal("committed invalid bytes")
			}
			if tc.size > 0 && spy.aborts != 1 {
				t.Fatal("multipart was not aborted")
			}
		})
	}
}

func TestCancellationAndFailuresAbort(t *testing.T) {
	sentinel := errors.New("network failed")
	for _, which := range []string{"part", "complete", "cancel", "abort-failed"} {
		t.Run(which, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			spy := &writeSpy{version: "v1"}
			switch which {
			case "part":
				spy.partError = sentinel
			case "complete":
				spy.completeError = sentinel
			case "cancel":
				spy.afterPart = cancel
			case "abort-failed":
				spy.partError = sentinel
				spy.abortError = errors.New("abort unavailable")
			}
			s := Store{api: spy, bucket: "test-bucket", partSize: minPartSize}
			receipt, err := s.Write(ctx, "test", 3, sha256.Sum256([]byte("abc")), bytes.NewBufferString("abc"))
			if err == nil || receipt != (Receipt{}) || spy.aborts != 1 || spy.abortContextError != nil {
				t.Fatalf("receipt=%+v err=%v spy=%+v", receipt, err, spy)
			}
			if which == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if which == "abort-failed" && !errors.Is(err, spy.abortError) {
				t.Fatalf("lost cleanup failure: %v", err)
			}
		})
	}
}

func TestWriteVersionsAndEmpty(t *testing.T) {
	for _, data := range []string{"", "abc"} {
		spy := &writeSpy{version: "physical-version"}
		s := Store{api: spy, bucket: "test-bucket", partSize: minPartSize}
		sum := sha256.Sum256([]byte(data))
		receipt, err := s.Write(context.Background(), "operation/part", int64(len(data)), sum, bytes.NewBufferString(data))
		if err != nil || receipt.VersionID != spy.version || receipt.Size != int64(len(data)) || receipt.SHA256 != sum {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
		if spy.aborts != 0 {
			t.Fatal("aborted completed multipart")
		}
		if data == "" && (spy.puts != 1 || spy.parts != 0) {
			t.Fatal("empty object must use single PUT")
		}
	}
	for _, version := range []string{"", "null"} {
		spy := &writeSpy{version: version}
		s := Store{api: spy, bucket: "test-bucket", partSize: minPartSize}
		receipt, err := s.Write(context.Background(), "test", 3, sha256.Sum256([]byte("abc")), bytes.NewBufferString("abc"))
		if !errors.Is(err, ErrUnversioned) || receipt != (Receipt{}) {
			t.Fatalf("version=%q receipt=%+v err=%v", version, receipt, err)
		}
	}
}

func TestCapacityFailureNeverProducesReceipt(t *testing.T) {
	for _, code := range []string{"XMinioStorageFull", "InsufficientStorage"} {
		for _, stage := range []string{"part", "complete"} {
			t.Run(code+"/"+stage, func(t *testing.T) {
				full := minio.ErrorResponse{Code: code, StatusCode: 507}
				spy := &writeSpy{version: "must-not-be-confirmed"}
				if stage == "part" {
					spy.partError = full
				} else {
					spy.completeError = full
				}
				s := Store{api: spy, bucket: "test-bucket", partSize: minPartSize}
				receipt, err := s.Write(context.Background(), "capacity", 3, sha256.Sum256([]byte("abc")), bytes.NewBufferString("abc"))
				var response minio.ErrorResponse
				if !errors.As(err, &response) || response.Code != code || receipt != (Receipt{}) || spy.aborts != 1 {
					t.Fatalf("capacity error lost or receipt issued: receipt=%+v err=%v aborts=%d", receipt, err, spy.aborts)
				}
				if stage == "part" && spy.completes != 0 {
					t.Fatal("attempted completion after part capacity failure")
				}
			})
		}
	}
}

func TestMultipartSizeDoesNotImposeReferenceFileLimit(t *testing.T) {
	for _, size := range []int64{0, 2 << 30, 3 << 30, DefaultPartSize*maxParts + 1, maxPartSize * maxParts} {
		part, err := multipartSize(size, DefaultPartSize)
		if err != nil || part < DefaultPartSize || part > maxPartSize {
			t.Fatalf("size=%d part=%d err=%v", size, part, err)
		}
		if size > 0 && (size-1)/part+1 > maxParts {
			t.Fatal("too many parts")
		}
	}
	for _, size := range []int64{-1, math.MaxInt64} {
		if _, err := multipartSize(size, DefaultPartSize); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid size accepted")
		}
	}
}

func TestNoNetworkForInvalidWrite(t *testing.T) {
	s := Store{partSize: minPartSize}
	for _, size := range []int64{-1, math.MaxInt64} {
		if _, err := s.Write(context.Background(), "test", size, [32]byte{}, bytes.NewReader(nil)); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Write(ctx, "test", 0, sha256.Sum256(nil), bytes.NewReader(nil)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := s.Open(context.Background(), "test", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
