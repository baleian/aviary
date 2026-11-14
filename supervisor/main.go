package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/store"
)

const (
	databaseURL     = "AVIARY_DATABASE_URL"
	valkeyURL       = "AVIARY_VALKEY_URL"
	listenAddr      = ":8080"
	shutdownTimeout = 10 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("supervisor stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := env(databaseURL)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, database)
	if err != nil {
		return err
	}
	defer st.Close()

	valkey, err := env(valkeyURL)
	if err != nil {
		return err
	}
	ev, err := events.Open(valkey)
	if err != nil {
		return err
	}
	defer ev.Close()

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           api.New(st, ev, ctx.Done()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	slog.Info("listening", "addr", listenAddr)

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	draining, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(draining); err != nil {
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
