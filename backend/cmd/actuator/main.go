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

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/faultcontrol"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fault actuator stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := faultcontrol.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	mapping, err := faultcontrol.NewMapping(config.Mode, config.Targets, config.ActuatorContainer)
	if err != nil {
		return err
	}
	var driver faultcontrol.Driver
	switch config.Mode {
	case faultcontrol.ModeDocker:
		driver, err = faultcontrol.NewDockerDriver(config.DockerSocket)
	case faultcontrol.ModeRailwaySSH:
		driver = faultcontrol.UnavailableDriver{Reason: faultcontrol.ErrRailwayUnavailable}
	}
	if err != nil {
		return err
	}
	service, err := faultcontrol.NewService(driver, mapping)
	if err != nil {
		return err
	}
	handler, err := faultcontrol.NewHandler(service, config.InternalToken)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", handler)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: fmt.Sprintf(":%d", config.Port), Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-result; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
