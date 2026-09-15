package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// ACERVO_TEST_S3_ENDPOINTS explicitly opts into real storage writes. The fixtures
// use unique prefixes and preserve all created versions for inspection.
func TestIntegrationLocalStorage(t *testing.T) {
	endpoints := os.Getenv("ACERVO_TEST_S3_ENDPOINTS")
	if endpoints == "" {
		t.Skip("set ACERVO_TEST_S3_ENDPOINTS to run real MinIO tests")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "adapter-proof/" + hex.EncodeToString(id[:])
	payload := bytes.Repeat([]byte("streamed-part-"), 600000) // >5 MiB, two multipart parts.
	for _, endpoint := range strings.Split(endpoints, ",") {
		t.Run(endpoint, func(t *testing.T) {
			s, err := New(Options{Endpoint: endpoint, AccessKey: "minioadmin", SecretKey: "minioadmin", Bucket: "drive-clone", PartSize: minPartSize})
			if err != nil {
				t.Fatal(err)
			}
			core := s.api.(*minio.Core)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			for _, tc := range []struct {
				name string
				body []byte
			}{{"empty", nil}, {"small", []byte("bytes")}, {"multipart", payload}} {
				t.Run(tc.name, func(t *testing.T) {
					sum := sha256.Sum256(tc.body)
					first, err := s.Write(ctx, prefix+"/"+tc.name, int64(len(tc.body)), sum, bytes.NewReader(tc.body))
					if err != nil {
						t.Fatal(err)
					}
					second, err := s.Write(ctx, prefix+"/"+tc.name, int64(len(tc.body)), sum, bytes.NewReader(tc.body))
					if err != nil {
						t.Fatal(err)
					}
					if first.Key != second.Key || first.VersionID == second.VersionID {
						t.Fatalf("repeated write lost version semantics: %+v %+v", first, second)
					}
					for _, receipt := range []Receipt{first, second} {
						r, info, err := s.Open(ctx, receipt.Key, receipt.VersionID)
						if err != nil {
							t.Fatal(err)
						}
						hash := sha256.New()
						size, err := io.Copy(hash, r)
						closeErr := r.Close()
						if err != nil || closeErr != nil || size != int64(len(tc.body)) || info.Size != size || !bytes.Equal(hash.Sum(nil), sum[:]) {
							t.Fatalf("download mismatch: size=%d info=%+v err=%v close=%v", size, info, err, closeErr)
						}
					}
					t.Logf("bytes=%d key=%s version=%s", first.Size, first.Key, first.VersionID)
				})
			}
			for _, mode := range []string{"hash", "extra", "short", "reader-error", "cancel"} {
				t.Run(mode, func(t *testing.T) {
					body := payload
					size := int64(len(body))
					sum := sha256.Sum256(body)
					var source io.Reader = bytes.NewReader(body)
					callCtx, callCancel := context.WithCancel(ctx)
					defer callCancel()
					switch mode {
					case "hash":
						sum = sha256.Sum256([]byte("wrong"))
					case "extra":
						size--
					case "short":
						size++
					case "reader-error":
						source = io.MultiReader(bytes.NewReader(body[:100]), failingReader{})
					case "cancel":
						source = &cancellingReader{r: bytes.NewReader(body), cancel: callCancel}
					}
					badPrefix := prefix + "/rejected-" + mode
					receipt, err := s.Write(callCtx, badPrefix, size, sum, source)
					if err == nil || receipt != (Receipt{}) {
						t.Fatalf("invalid source produced receipt: %+v err=%v", receipt, err)
					}
					key := badPrefix + "/" + hex.EncodeToString(sum[:])
					versions, err := core.ListObjectsV2("drive-clone", key, "", "", "", 10)
					if err != nil {
						t.Fatal(err)
					}
					if len(versions.Contents) != 0 {
						t.Fatal("invalid content became visible")
					}
					uploads, err := core.ListMultipartUploads(ctx, "drive-clone", badPrefix, "", "", "", 1000)
					if err != nil {
						t.Fatal(err)
					}
					for _, upload := range uploads.Uploads {
						if strings.HasPrefix(upload.Key, badPrefix) {
							t.Fatalf("multipart not aborted: %+v", upload)
						}
					}
				})
			}
			if err := s.Check(ctx); err != nil {
				t.Fatalf("local health write/read/cleanup: %v", err)
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("source failed") }

type cancellingReader struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.cancel()
	return n, err
}
