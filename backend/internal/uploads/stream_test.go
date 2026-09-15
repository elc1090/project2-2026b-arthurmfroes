package uploads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedDownload struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func (r *blockedDownload) Read(p []byte) (int, error) {
	close(r.started)
	<-r.closed
	copy(p, "late")
	return min(len(p), 4), io.EOF
}
func (r *blockedDownload) Close() error {
	r.closes.Add(1)
	r.once.Do(func() { close(r.closed) })
	return nil
}
func waitStream(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("download goroutine did not stop")
	}
}

func TestGuardStreamClosesBlockedReadAfterAuthorityLoss(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &blockedDownload{started: make(chan struct{}), closed: make(chan struct{})}
	denied := errors.New("node lost SQL authority")
	reader := newGuardReadCloser(ctx, cancel, source, time.Millisecond, func(ctx context.Context) error {
		select {
		case <-source.started:
			return denied
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer reader.Close()
	result := make(chan struct {
		n   int
		err error
	}, 1)
	go func() {
		var data [4]byte
		n, err := reader.Read(data[:])
		result <- struct {
			n   int
			err error
		}{n, err}
	}()
	select {
	case got := <-result:
		if got.n != 0 || !errors.Is(got.err, denied) {
			t.Fatal("authority loss looked like successful EOF", got)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked origin was not closed")
	}
	waitStream(t, reader.done)
	if ctx.Err() != context.Canceled {
		t.Fatal("S3 request context not cancelled")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if source.closes.Load() != 1 {
		t.Fatal("origin closed more than once", source.closes.Load())
	}
}

func TestGuardStreamCloseStopsActiveCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered, returned := make(chan struct{}), make(chan struct{})
	source := &blockedDownload{started: make(chan struct{}), closed: make(chan struct{})}
	reader := newGuardReadCloser(ctx, cancel, source, time.Millisecond, func(ctx context.Context) error { close(entered); <-ctx.Done(); close(returned); return ctx.Err() })
	waitStream(t, entered)
	closed := make(chan struct{})
	go func() { _ = reader.Close(); close(closed) }()
	waitStream(t, closed)
	waitStream(t, returned)
	waitStream(t, reader.done)
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if source.closes.Load() != 1 {
		t.Fatal(source.closes.Load())
	}
}

type finiteDownload struct {
	*bytes.Reader
	closed atomic.Int32
}

func (r *finiteDownload) Close() error { r.closed.Add(1); return nil }
func TestGuardStreamNormalEOFStopsMonitor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &finiteDownload{Reader: bytes.NewReader([]byte("download bytes"))}
	reader := newGuardReadCloser(ctx, cancel, source, time.Hour, func(context.Context) error { t.Error("unexpected authority tick"); return nil })
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "download bytes" {
		t.Fatal(string(body), err)
	}
	waitStream(t, reader.done)
	if source.closed.Load() != 1 {
		t.Fatal("EOF did not close origin")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if source.closed.Load() != 1 {
		t.Fatal("duplicate origin Close")
	}
}
func TestGuardStreamParentCancellationClosesOrigin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &blockedDownload{started: make(chan struct{}), closed: make(chan struct{})}
	reader := newGuardReadCloser(ctx, cancel, source, time.Hour, func(context.Context) error { return nil })
	cancel()
	waitStream(t, reader.done)
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if source.closes.Load() != 1 {
		t.Fatal(source.closes.Load())
	}
}
