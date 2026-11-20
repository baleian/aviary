// Package app is the Supervisor as a program: it reads its settings from the
// environment, connects to the database, the event log and the secret
// stores with the plugins of the build, and serves. A build of the
// Supervisor is a main package that calls Main with its plugins.
package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/rest"

	"aviary/supervisor/events"
	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/resolver"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

const (
	databaseURL        = "AVIARY_DATABASE_URL"
	eventsURL          = "AVIARY_EVENTS_URL"
	encryptionKeys     = "AVIARY_ENCRYPTION_KEYS"
	encryptionKey      = "AVIARY_ENCRYPTION_KEY"
	secretStoreHosts   = "AVIARY_SECRET_STORE_HOSTS"
	secretCacheSeconds = "AVIARY_SECRET_CACHE_SECONDS"
	identityProvider   = "AVIARY_IDENTITY_PROVIDER"
	adminGroups        = "AVIARY_ADMIN_GROUPS"
	userGroups         = "AVIARY_USER_GROUPS"
	harnesses          = "AVIARY_HARNESSES"
	maxConcurrentTurns = "AVIARY_MAX_CONCURRENT_TURNS"
	maxQueuedTurns     = "AVIARY_MAX_QUEUED_TURNS"
	maxQueueWait       = "AVIARY_MAX_QUEUE_WAIT_SECONDS"
	maxStartup         = "AVIARY_MAX_STARTUP_SECONDS"
	maxToolOutput      = "AVIARY_MAX_TOOL_OUTPUT_BYTES"
	maxTurn            = "AVIARY_MAX_TURN_SECONDS"
	sandboxNamespace   = "AVIARY_SANDBOX_NAMESPACE"
	sandboxVolume      = "AVIARY_SANDBOX_VOLUME_SIZE"
	sandboxClass       = "AVIARY_SANDBOX_STORAGE_CLASS"
	sandboxIdle        = "AVIARY_SANDBOX_IDLE_SECONDS"
	resolverTLS        = "AVIARY_RESOLVER_TLS"
	sandboxRetention   = "AVIARY_SANDBOX_RETENTION_SECONDS"
	apiAddr            = ":8080"
	metricsAddr        = ":9092"
	resolverAddr       = ":9443"
	// shutdownTimeout is how long the requests to the API get to finish.
	shutdownTimeout = 10 * time.Second
	// identityPatience is how long the identity provider may take to answer
	// when the Supervisor starts.
	identityPatience = 30 * time.Second
)

// handover is how long a Supervisor that was told to stop goes on serving.
// For that long the cluster may still send it new requests. The tests set it
// lower.
var handover = 5 * time.Second

// ports are what a Supervisor listens on.
type ports struct {
	api, metrics net.Listener
	// resolver is where the egress of the sandbox service asks about the
	// requests of the sandboxes, over TLS.
	resolver net.Listener
}

// Plugins are the plugins of a build: what connects its Supervisor to the
// data that an installation keeps outside it. An installation says which of
// them it uses with the addresses that it gives the Supervisor, and its
// projects with the kinds of their secret stores.
type Plugins struct {
	// Databases connect to the database that holds the records. The scheme
	// of the database's URL says which of them does.
	Databases []store.Plugin
	// EventLogs connect to the log of the turns' events, likewise.
	EventLogs []events.Plugin
	// SecretStores ask the secret stores of the projects, each those of its
	// kind.
	SecretStores []secret.Plugin
	// IdentityProviders ask the identity provider of the installation who
	// sends a request. The kind of the provider says which of them does.
	IdentityProviders []identity.Plugin
}

// Main runs the Supervisor with the arguments of the program until the
// program is told to stop, and ends the program.
func Main(plugins Plugins) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, os.Args[1:], plugins); err != nil {
		slog.Error("supervisor stopped", "error", err)
		stop()
		os.Exit(1)
	}
}

// Run serves until ctx is done, or with the argument "migrate" brings the
// database schema up to date. A release migrates once, before it starts its
// Supervisors. With a version after it, "migrate" moves the schema to that
// version instead, forward or back.
func Run(ctx context.Context, args []string, plugins Plugins) error {
	database, err := env(databaseURL)
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		return serve(ctx, database, plugins)
	case len(args) == 1 && args[0] == "migrate":
		return store.Migrate(ctx, database, plugins.Databases...)
	case len(args) == 2 && args[0] == "migrate":
		version, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not a schema version", args[1])
		}
		return store.MigrateTo(ctx, database, version, plugins.Databases...)
	}
	return errors.New("usage: aviary-supervisor [migrate [<version>]]")
}

// sandboxSettings reads where the sandboxes of the sessions are made, in
// the cluster that the Supervisor runs in, and what they are made with.
func sandboxSettings() (sandbox.Settings, error) {
	var settings sandbox.Settings
	var err error
	if settings.Namespace, err = env(sandboxNamespace); err != nil {
		return settings, err
	}
	size, err := env(sandboxVolume)
	if err != nil {
		return settings, err
	}
	if settings.Volume, err = resource.ParseQuantity(size); err != nil || settings.Volume.Sign() <= 0 {
		return settings, fmt.Errorf("%s must be a size such as 10Gi", sandboxVolume)
	}
	// Without a class, the volumes are of the default class of the cluster.
	settings.StorageClass = os.Getenv(sandboxClass)
	output, err := env(maxToolOutput)
	if err != nil {
		return settings, err
	}
	if settings.MaxToolOutputBytes, err = strconv.Atoi(output); err != nil || settings.MaxToolOutputBytes < 0 {
		return settings, fmt.Errorf("%s must be a number of bytes, or zero for no limit", maxToolOutput)
	}
	return settings, nil
}

func serve(ctx context.Context, database string, plugins Plugins) error {
	st, err := store.Open(ctx, database, plugins.Databases...)
	if err != nil {
		return err
	}
	defer st.Close()

	address, err := env(eventsURL)
	if err != nil {
		return err
	}
	ev, err := events.Open(address, plugins.EventLogs...)
	if err != nil {
		return err
	}
	defer ev.Close()

	keys, err := encryption()
	if err != nil {
		return err
	}
	stores, err := allowedSecretStores(plugins.SecretStores)
	if err != nil {
		return err
	}
	defer stores.Close()
	callers, err := callersOf(ctx, plugins.IdentityProviders)
	if err != nil {
		return err
	}
	config, err := driverConfig()
	if err != nil {
		return err
	}
	settings, err := sandboxSettings()
	if err != nil {
		return err
	}
	cluster, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	client, err := sandbox.New(cluster, settings)
	if err != nil {
		return err
	}

	var on ports
	if on.api, err = net.Listen("tcp", apiAddr); err != nil {
		return err
	}
	if on.metrics, err = net.Listen("tcp", metricsAddr); err != nil {
		return err
	}
	if on.resolver, err = resolverListener(); err != nil {
		return err
	}
	return serveOn(ctx, on, st, ev, client, config, keys, stores, callers, slog.Default())
}

// resolverListener listens for the egress of the sandbox service with the
// certificate and the key in the directory that is named, as tls.crt and
// tls.key: the egress asks only over TLS.
func resolverListener() (net.Listener, error) {
	dir, err := env(resolverTLS)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		return nil, fmt.Errorf("the certificate of the resolver: %w", err)
	}
	listener, err := net.Listen("tcp", resolverAddr)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}), nil
}

// serveOn serves on the ports until ctx is done, with the turns in the
// database of the store and their sandboxes kept by sandboxes. It seals the
// secrets that it stores with the keys, and reads the others from the
// stores. The callers are who may use the API. The resolver writes the
// records of the requests of the sandboxes to records.
func serveOn(ctx context.Context, on ports, st store.Store, ev *events.Streams, sandboxes driver.Sandboxes, config driver.Config, keys *secret.Keys, stores *secret.Stores, callers api.Callers, records *slog.Logger) error {
	m := metrics.New()
	stores.Asked = m.SecretRead
	answers := resolver.New(st, keys, stores, records)
	dr := driver.New(config, sandboxes, st, ev, m)
	resolverServer := &http.Server{Handler: answers.Handler(), ReadHeaderTimeout: 10 * time.Second}
	apiServer := &http.Server{
		Handler:           api.New(st, ev, dr, keys, stores, callers, ctx.Done()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// The metrics are on a port of their own, which the clients of the API
	// have no way to. They are served until the Supervisor has stopped.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", m.Handler())
	metricsServer := &http.Server{Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}
	defer metricsServer.Close()
	failed := make(chan error, 3)
	go func() { failed <- apiServer.Serve(on.api) }()
	go func() { failed <- resolverServer.Serve(on.resolver) }()
	go func() { failed <- metricsServer.Serve(on.metrics) }()
	go dr.Run(ctx)
	slog.Info("listening", "api", on.api.Addr().String(), "resolver", on.resolver.Addr().String(),
		"metrics", on.metrics.Addr().String())

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	// A Supervisor that is told to stop takes on no more turns and follows
	// none. The other Supervisors go on, and take up the turns that it
	// followed.
	slog.Info("stopping")
	time.Sleep(handover)
	stopping, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return errors.Join(apiServer.Shutdown(stopping), resolverServer.Shutdown(stopping))
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

// allowedSecretStores reads the hosts at which the installation allows the
// secret stores of the projects, a JSON list such as
// ["secrets.example.com:8200"], and for how many seconds a value that was
// read from a store is used before it is read again. The stores are asked
// with the plugins.
func allowedSecretStores(plugins []secret.Plugin) (*secret.Stores, error) {
	encoded, err := env(secretStoreHosts)
	if err != nil {
		return nil, err
	}
	var hosts []string
	if err := json.Unmarshal([]byte(encoded), &hosts); err != nil {
		return nil, fmt.Errorf("%s: %w", secretStoreHosts, err)
	}
	written, err := env(secretCacheSeconds)
	if err != nil {
		return nil, err
	}
	seconds, err := strconv.Atoi(written)
	if err != nil || seconds < 0 {
		return nil, fmt.Errorf("%s must be a number of seconds, or zero to read a store every time", secretCacheSeconds)
	}
	return secret.NewStores(hosts, time.Duration(seconds)*time.Second, plugins...)
}

// callersOf reads who may use the API. The identity provider of the
// installation is a JSON object such as {"kind": "oidc", "settings": {...}},
// and is connected to with the plugin for its kind. The groups of the
// provider whose members are administrators, and those whose members may use
// the API, are JSON lists such as ["platform-team"]. An installation that
// names no kind has no provider: nobody who sends a request is asked who
// they are, and the groups mean nothing.
func callersOf(ctx context.Context, plugins []identity.Plugin) (api.Callers, error) {
	var callers api.Callers
	encoded, err := env(identityProvider)
	if err != nil {
		return callers, err
	}
	var provider struct {
		Kind     string          `json:"kind"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal([]byte(encoded), &provider); err != nil {
		return callers, fmt.Errorf("%s: %w", identityProvider, err)
	}
	for name, groups := range map[string]*[]string{adminGroups: &callers.Admins, userGroups: &callers.Users} {
		encoded, err := env(name)
		if err != nil {
			return callers, err
		}
		if err := json.Unmarshal([]byte(encoded), groups); err != nil {
			return callers, fmt.Errorf("%s: %w", name, err)
		}
	}
	if provider.Kind == "" {
		return api.Callers{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, identityPatience)
	defer cancel()
	callers.Provider, err = identity.Open(ctx, provider.Kind, provider.Settings, plugins...)
	return callers, err
}

// driverConfig reads how turns are run.
func driverConfig() (driver.Config, error) {
	var config driver.Config
	// The harnesses are a JSON object such as {"opencode": {"image":
	// "registry.example.com/harness-opencode:1.0", "resources": {"requests":
	// {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "4Gi"}}}}.
	encoded, err := env(harnesses)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal([]byte(encoded), &config.Harnesses); err != nil {
		return config, fmt.Errorf("%s: %w", harnesses, err)
	}
	for name, harness := range config.Harnesses {
		if harness.Image == "" {
			return config, fmt.Errorf("%s: the harness %s has no image", harnesses, name)
		}
	}
	idle, err := env(sandboxIdle)
	if err != nil {
		return config, err
	}
	seconds, err := strconv.Atoi(idle)
	if err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or zero for never", sandboxIdle)
	}
	config.Idle = time.Duration(seconds) * time.Second
	retention, err := env(sandboxRetention)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(retention); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or zero for until the session is ended", sandboxRetention)
	}
	config.Retention = time.Duration(seconds) * time.Second
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
	if seconds, err = strconv.Atoi(wait); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or zero for no limit", maxQueueWait)
	}
	config.MaxQueueWait = time.Duration(seconds) * time.Second
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
	return config, nil
}

func env(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
