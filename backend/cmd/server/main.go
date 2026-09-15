package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/config"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/database"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	localStorage, err := storage.New(storage.Options{Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket})
	if err != nil {
		return err
	}
	state := &runtimeState{identity: identityHandler(cfg, pool)}
	initialized := make(chan struct{})
	go func() { defer close(initialized); initialize(ctx, cfg, pool, localStorage, state) }()
	defer func() { stop(); <-initialized }()
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           newHandler(state),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	result := make(chan error, 1)
	go func() { result <- srv.ListenAndServe() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("HTTP shutdown: %w", err)
	}
	if err := <-result; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newHandler(state *runtimeState) http.Handler {
	mux := http.NewServeMux()
	if state != nil && state.identity != nil {
		mux.Handle("GET /internal/node/identity", state.identity)
	}
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("alive\n"))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if state != nil {
			if runtime := state.current.Load(); runtime != nil {
				check, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				defer cancel()
				if runtime.controller.Eligible(check) == nil {
					w.Header().Set("Cache-Control", "no-store")
					w.WriteHeader(http.StatusOK)
					return
				}
			}
		}
		// A live process alone cannot prove local health or cluster admission.
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "node readiness is not available", http.StatusServiceUnavailable)
	})
	for _, route := range []string{"/api/", "/internal/"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if state != nil {
				if runtime := state.current.Load(); runtime != nil {
					if r.URL.Path[:5] == "/api/" {
						runtime.api.ServeHTTP(w, r)
					} else {
						runtime.internal.ServeHTTP(w, r)
					}
					return
				}
			}
			http.Error(w, "node initializing", http.StatusServiceUnavailable)
		})
	}
	return mux
}
