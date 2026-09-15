package uploads

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// guardReadCloser keeps an already-open download subject to node authority.
// Closing the origin must interrupt its blocked Read, as HTTP response bodies do.
// The check must respect ctx; the service supplies its bounded metadata transaction.
type guardReadCloser struct {
	source   io.ReadCloser
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	terminal error
	closeErr error
}

func newGuardReadCloser(ctx context.Context, cancel context.CancelFunc, source io.ReadCloser, interval time.Duration, check func(context.Context) error) *guardReadCloser {
	reader := &guardReadCloser{source: source, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(reader.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				reader.stop(ctx.Err())
				return
			case <-ticker.C:
				if err := check(ctx); err != nil {
					reader.stop(err)
					return
				}
			}
		}
	}()
	return reader
}

func (r *guardReadCloser) stop(reason error) {
	r.mu.Lock()
	if r.terminal == nil {
		r.terminal = reason
	}
	r.mu.Unlock()
	r.once.Do(func() {
		r.cancel()
		err := r.source.Close()
		r.mu.Lock()
		r.closeErr = err
		r.mu.Unlock()
	})
}
func (r *guardReadCloser) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminal
}
func (r *guardReadCloser) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.stop(err)
	}
	if err := r.failure(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(p)
	// A failed authority check can close a blocked Read. Preserve that failure
	// instead of treating the origin's resulting EOF as successful completion.
	if failure := r.failure(); failure != nil {
		return 0, failure
	}
	if err != nil {
		r.stop(err)
		<-r.done
		if failure := r.failure(); !errors.Is(failure, err) {
			return 0, failure
		}
	}
	return n, err
}
func (r *guardReadCloser) Close() error {
	r.stop(io.ErrClosedPipe)
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeErr
}
