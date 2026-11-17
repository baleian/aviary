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
	"google.golang.org/grpc/keepalive"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/authz"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/gateway"
	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/secret"
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
	encryptionKeys     = "AVIARY_ENCRYPTION_KEYS"
	encryptionKey      = "AVIARY_ENCRYPTION_KEY"
	harnesses          = "AVIARY_HARNESSES"
	maxConcurrentTurns = "AVIARY_MAX_CONCURRENT_TURNS"
	maxQueuedTurns     = "AVIARY_MAX_QUEUED_TURNS"
	maxQueueWait       = "AVIARY_MAX_QUEUE_WAIT_SECONDS"
	maxReconnect       = "AVIARY_MAX_RECONNECT_SECONDS"
	maxStartup         = "AVIARY_MAX_STARTUP_SECONDS"
	maxToolOutput      = "AVIARY_MAX_TOOL_OUTPUT_BYTES"
	maxTurn            = "AVIARY_MAX_TURN_SECONDS"
	apiAddr            = ":8080"
	gatewayAddr        = ":9090"
	authzAddr          = ":9091"
	metricsAddr        = ":9092"
	// shutdownTimeout is how long the requests to the API get to finish.
	shutdownTimeout = 10 * time.Second
	// keepaliveInterval is how often the Supervisor asks the other end of an
	// idle connection whether it is still there, and how long it waits for
	// the answer. The runners and the Egress Proxy ask the same of it.
	keepaliveInterval = 30 * time.Second
)

// handover is how long a Supervisor that was told to stop goes on serving.
// For that long the cluster may still send it new requests. The tests set it
// lower.
var handover = 5 * time.Second

// ports are what a Supervisor listens on.
type ports struct {
	api, gateway, authz, metrics net.Listener
}

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

	keys, err := encryption()
	if err != nil {
		return err
	}
	config, err := driverConfig()
	if err != nil {
		return err
	}
	client, err := driver.InCluster()
	if err != nil {
		return err
	}

	var on ports
	if on.api, err = net.Listen("tcp", apiAddr); err != nil {
		return err
	}
	if on.gateway, err = net.Listen("tcp", gatewayAddr); err != nil {
		return err
	}
	if on.authz, err = net.Listen("tcp", authzAddr); err != nil {
		return err
	}
	if on.metrics, err = net.Listen("tcp", metricsAddr); err != nil {
		return err
	}
	return serveOn(ctx, on, st, ev, client, config, keys, gateway.Sweep)
}

// serveOn serves on the ports until ctx is done, with the turns in the
// database of the store and their pods in the cluster of the client. It
// seals the secrets that it stores with the keys. Every sweep it looks for
// what the runners are to be told and no Supervisor announced.
func serveOn(ctx context.Context, on ports, st *store.Store, ev *events.Streams, client kubernetes.Interface, config driver.Config, keys *secret.Keys, sweep time.Duration) error {
	m := metrics.New()
	dr, err := driver.New(config, client, st, ev, m)
	if err != nil {
		return err
	}
	runners := gateway.New(st, ev, m, sweep)
	apiServer := &http.Server{
		Handler:           api.New(st, ev, dr, keys, ctx.Done()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// A machine that is gone closes no connection. Both ends of a connection
	// find that out by asking, and close it.
	alive := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: keepaliveInterval, Timeout: keepaliveInterval}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveInterval / 2,
			PermitWithoutStream: true,
		}),
	}
	gatewayServer := grpc.NewServer(alive...)
	runnerv1.RegisterRunnerServiceServer(gatewayServer, runners)
	// The Egress Proxy asks on a port of its own, so that the turn pods can
	// be kept from it.
	authzServer := grpc.NewServer(alive...)
	authv3.RegisterAuthorizationServer(authzServer, authz.New(st))
	// The metrics are on a port of their own, which the clients of the API
	// and the turn pods have no way to. They are served until the Supervisor
	// has stopped.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", m.Handler())
	metricsServer := &http.Server{Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}
	defer metricsServer.Close()
	failed := make(chan error, 4)
	go func() { failed <- apiServer.Serve(on.api) }()
	go func() { failed <- gatewayServer.Serve(on.gateway) }()
	go func() { failed <- authzServer.Serve(on.authz) }()
	go func() { failed <- metricsServer.Serve(on.metrics) }()
	go dr.Run(ctx)
	// The runners are served for a while after the Supervisor was told to
	// stop.
	serving, served := context.WithCancel(context.Background())
	defer served()
	go runners.Run(serving)
	slog.Info("listening", "api", on.api.Addr().String(), "gateway", on.gateway.Addr().String(),
		"authz", on.authz.Addr().String(), "metrics", on.metrics.Addr().String())

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	// A Supervisor that is told to stop takes on no more turns, and the other
	// Supervisors go on.
	slog.Info("stopping")
	time.Sleep(handover)
	stopping, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = apiServer.Shutdown(stopping)
	// The runners call another Supervisor. GracefulStop would wait for
	// their turns to end.
	gatewayServer.Stop()
	authzServer.GracefulStop()
	return err
}

// encryption reads the keys that secrets are sealed with: the directory
// that holds them, and the name of the one that seals.
func encryption() (*secret.Keys, error) {
	dir, err := env(encryptionKeys)
	if err != nil {
		return nil, err
	}
	current, err := env(encryptionKey)
	if err != nil {
		return nil, err
	}
	return secret.Load(dir, current)
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
	queued, err := env(maxQueuedTurns)
	if err != nil {
		return config, err
	}
	if config.MaxQueuedTurns, err = strconv.Atoi(queued); err != nil || config.MaxQueuedTurns < 0 {
		return config, fmt.Errorf("%s must be a number, or zero for no limit", maxQueuedTurns)
	}
	wait, err := env(maxQueueWait)
	if err != nil {
		return config, err
	}
	seconds, err := strconv.Atoi(wait)
	if err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or zero for no limit", maxQueueWait)
	}
	config.MaxQueueWait = time.Duration(seconds) * time.Second
	output, err := env(maxToolOutput)
	if err != nil {
		return config, err
	}
	if config.MaxToolOutputBytes, err = strconv.Atoi(output); err != nil || config.MaxToolOutputBytes < 0 {
		return config, fmt.Errorf("%s must be a number of bytes, or zero for no limit", maxToolOutput)
	}
	startup, err := env(maxStartup)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(startup); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or zero for no limit", maxStartup)
	}
	config.MaxStartup = time.Duration(seconds) * time.Second
	longest, err := env(maxTurn)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(longest); err != nil || seconds < 1 {
		return config, fmt.Errorf("%s must be a number of seconds above zero", maxTurn)
	}
	config.MaxTurn = time.Duration(seconds) * time.Second
	reconnect, err := env(maxReconnect)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(reconnect); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or zero for no limit", maxReconnect)
	}
	config.MaxReconnect = time.Duration(seconds) * time.Second
	return config, nil
}

func env(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
