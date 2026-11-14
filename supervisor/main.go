package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/authz"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/gateway"
	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
	"aviary/supervisor/internal/store"
)

const (
	databaseURL        = "AVIARY_DATABASE_URL"
	valkeyURL          = "AVIARY_VALKEY_URL"
	gatewayURL         = "AVIARY_GATEWAY_URL"
	runnerImage        = "AVIARY_RUNNER_IMAGE"
	llmBaseURL         = "AVIARY_LLM_BASE_URL"
	llmModel           = "AVIARY_LLM_MODEL"
	proxyURL           = "AVIARY_PROXY_URL"
	harnesses          = "AVIARY_HARNESSES"
	maxConcurrentTurns = "AVIARY_MAX_CONCURRENT_TURNS"
	apiAddr            = ":8080"
	gatewayAddr        = ":9090"
	authzAddr          = ":9091"
	shutdownTimeout    = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	// The Kubernetes client logs through klog.
	klog.SetSlogLogger(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("supervisor stopped", "error", err)
		stop()
		os.Exit(1)
	}
}

// run serves, or with the argument "migrate" brings the database schema up
// to date. A release migrates once, before it starts its Supervisors. With
// a version after it, "migrate" moves the schema to that version instead,
// forward or back.
func run(ctx context.Context, args []string) error {
	database, err := env(databaseURL)
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		return serve(ctx, database)
	case len(args) == 1 && args[0] == "migrate":
		return store.Migrate(ctx, database)
	case len(args) == 2 && args[0] == "migrate":
		version, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not a schema version", args[1])
		}
		return store.MigrateTo(ctx, database, version)
	}
	return errors.New("usage: aviary-supervisor [migrate [<version>]]")
}

func serve(ctx context.Context, database string) error {
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

	config, err := driverConfig()
	if err != nil {
		return err
	}
	dr, err := driver.New(config, st, ev)
	if err != nil {
		return err
	}

	apiListener, err := net.Listen("tcp", apiAddr)
	if err != nil {
		return err
	}
	gatewayListener, err := net.Listen("tcp", gatewayAddr)
	if err != nil {
		return err
	}
	authzListener, err := net.Listen("tcp", authzAddr)
	if err != nil {
		return err
	}
	apiServer := &http.Server{
		Handler:           api.New(st, ev, dr, ctx.Done()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	gatewayServer := grpc.NewServer()
	runnerv1.RegisterRunnerServiceServer(gatewayServer, gateway.New(st, ev))
	// The Egress Proxy asks on a port of its own, so that the turn pods can
	// be kept from it.
	authzServer := grpc.NewServer()
	authv3.RegisterAuthorizationServer(authzServer, authz.New(st))
	failed := make(chan error, 3)
	go func() { failed <- apiServer.Serve(apiListener) }()
	go func() { failed <- gatewayServer.Serve(gatewayListener) }()
	go func() { failed <- authzServer.Serve(authzListener) }()
	go dr.Run(ctx)
	slog.Info("listening", "api", apiAddr, "gateway", gatewayAddr, "authz", authzAddr)

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	draining, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// A turn can run for longer than the Supervisor may take to stop, so the
	// runners that are still connected when the time is up are cut off.
	go func() {
		<-draining.Done()
		gatewayServer.Stop()
	}()
	err = apiServer.Shutdown(draining)
	gatewayServer.GracefulStop()
	// The turns that still run call the model until their runners are gone.
	authzServer.GracefulStop()
	return err
}

// driverConfig reads how turns are run. The harnesses are a JSON object such
// as {"opencode": {"image": "..."}}.
func driverConfig() (driver.Config, error) {
	var config driver.Config
	var err error
	if config.GatewayURL, err = env(gatewayURL); err != nil {
		return config, err
	}
	if config.RunnerImage, err = env(runnerImage); err != nil {
		return config, err
	}
	if config.LLMBaseURL, err = env(llmBaseURL); err != nil {
		return config, err
	}
	// An installation without a model endpoint names no model.
	config.LLMModel = os.Getenv(llmModel)
	if config.ProxyURL, err = env(proxyURL); err != nil {
		return config, err
	}
	encoded, err := env(harnesses)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal([]byte(encoded), &config.Harnesses); err != nil {
		return config, fmt.Errorf("%s: %w", harnesses, err)
	}
	limit, err := env(maxConcurrentTurns)
	if err != nil {
		return config, err
	}
	if config.MaxConcurrentTurns, err = strconv.Atoi(limit); err != nil || config.MaxConcurrentTurns < 1 {
		return config, fmt.Errorf("%s must be a number above zero", maxConcurrentTurns)
	}
	return config, nil
}

func env(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
