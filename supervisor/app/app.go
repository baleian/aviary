// Package app runs the Supervisor. It reads its settings from the
// environment and connects to the database, the event log, the secret stores
// and the identity provider through the plugins of the build. A build is a
// main package that calls Main with its plugins.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/rest"

	"aviary/supervisor/events"
	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/authz"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/metrics"
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
	sandboxRetention   = "AVIARY_SANDBOX_RETENTION_SECONDS"
	modelsURL          = "AVIARY_LLM_BASE_URL"
	proxyURL           = "AVIARY_PROXY_URL"
	apiAddr            = ":8080"
	authzAddr          = ":9091"
	metricsAddr        = ":9092"
	// shutdownTimeout is how long in-flight API requests get to finish.
	shutdownTimeout = 10 * time.Second
	// identityPatience bounds connecting to the identity provider at startup.
	identityPatience = 30 * time.Second
	// keepaliveInterval is the interval and the timeout of the keepalive
	// pings of the authz server.
	keepaliveInterval = 30 * time.Second
)

// handover is how long a stopping Supervisor keeps serving, since the cluster
// may still send it new requests. It is a variable for tests.
var handover = 5 * time.Second

type ports struct {
	api, authz, metrics net.Listener
}

// Plugins connect the Supervisor to the database, the event log, secret
// stores and identity providers. An installation selects among them by the
// scheme of each URL and by the kind of each secret store and identity
// provider.
type Plugins struct {
	Databases         []store.Plugin
	EventLogs         []events.Plugin
	SecretStores      []secret.Plugin
	IdentityProviders []identity.Plugin
}

// Main runs the Supervisor with the command-line arguments until SIGINT or
// SIGTERM, and exits with status 1 if it fails.
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

// Run serves until ctx is done. With the arguments "migrate [<version>]", it
// instead migrates the database schema to the latest or the given version,
// up or down. Serving does not migrate: a release runs "migrate" once, before
// it starts the Supervisors.
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
			return fmt.Errorf("invalid schema version %q", args[1])
		}
		return store.MigrateTo(ctx, database, version, plugins.Databases...)
	}
	return errors.New("usage: aviary-supervisor [migrate [<version>]]")
}

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
		return settings, fmt.Errorf("%s must be a positive quantity such as 10Gi", sandboxVolume)
	}
	// Optional: empty selects the default storage class of the cluster.
	settings.StorageClass = os.Getenv(sandboxClass)
	output, err := env(maxToolOutput)
	if err != nil {
		return settings, err
	}
	if settings.MaxToolOutputBytes, err = strconv.Atoi(output); err != nil || settings.MaxToolOutputBytes < 0 {
		return settings, fmt.Errorf("%s must be a number of bytes, or 0 for no limit", maxToolOutput)
	}
	if settings.Proxy, err = address(proxyURL); err != nil {
		return settings, err
	}
	settings.Models, err = address(modelsURL)
	return settings, err
}

// address reads an Egress Proxy URL from the named variable.
func address(name string) (*url.URL, error) {
	value, err := env(name)
	if err != nil {
		return nil, err
	}
	at, err := url.Parse(value)
	if err != nil || at.Scheme != "http" || at.Hostname() == "" {
		return nil, fmt.Errorf("%s must be a URL such as http://aviary-egress-proxy.aviary-system:8080", name)
	}
	return at, nil
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
	if on.authz, err = net.Listen("tcp", authzAddr); err != nil {
		return err
	}
	if on.metrics, err = net.Listen("tcp", metricsAddr); err != nil {
		return err
	}
	return serveOn(ctx, on, st, ev, client, config, keys, stores, callers)
}

// serveOn serves on the listeners until ctx is done. keys seal the stored
// secrets and the session tokens, stores read the secrets kept in secret
// stores, and callers says who may use the API.
func serveOn(ctx context.Context, on ports, st store.Store, ev *events.Streams, sandboxes driver.Sandboxes, config driver.Config, keys *secret.Keys, stores *secret.Stores, callers api.Callers) error {
	m := metrics.New(slices.Sorted(maps.Keys(config.Harnesses)), stores.Kinds())
	stores.Asked = m.SecretRead
	config.Token = func(session uuid.UUID) (string, error) { return authz.Token(keys, session) }
	dr := driver.New(config, sandboxes, st, ev, m)
	apiServer := &http.Server{
		Handler:           api.New(st, ev, dr, keys, stores, callers, ctx.Done()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Authz checks have a port of their own, which can be closed to the
	// sandboxes. A peer that disappears does not close its connections, so
	// both ends send keepalive pings: the server sends its own and permits
	// those of the Egress Proxy.
	authzServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: keepaliveInterval, Timeout: keepaliveInterval}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveInterval / 2,
			PermitWithoutStream: true,
		}),
	)
	authv3.RegisterAuthorizationServer(authzServer, authz.New(st, keys, stores))
	// Metrics have a port of their own, so that they are not exposed with the
	// API. They are served until serveOn returns.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", m.Handler())
	metricsServer := &http.Server{Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}
	defer metricsServer.Close()
	failed := make(chan error, 3)
	go func() { failed <- apiServer.Serve(on.api) }()
	go func() { failed <- authzServer.Serve(on.authz) }()
	go func() { failed <- metricsServer.Serve(on.metrics) }()
	go dr.Run(ctx)
	slog.Info("listening", "api", on.api.Addr().String(), "authz", on.authz.Addr().String(),
		"metrics", on.metrics.Addr().String())

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	// The driver stops with ctx, and the other Supervisors take over its
	// turns. The API keeps serving for the handover, then drains.
	slog.Info("stopping")
	time.Sleep(handover)
	stopping, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := apiServer.Shutdown(stopping)
	authzServer.GracefulStop()
	return err
}

// encryption loads the keys from the directory in AVIARY_ENCRYPTION_KEYS.
// AVIARY_ENCRYPTION_KEY names the key that new secrets are sealed with.
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

// allowedSecretStores reads the hosts at which secret stores are allowed, a
// JSON list such as ["secrets.example.com:8200"], and the number of seconds
// that values read from a store are cached.
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
		return nil, fmt.Errorf("%s must be a number of seconds, or 0 to disable caching", secretCacheSeconds)
	}
	return secret.NewStores(hosts, time.Duration(seconds)*time.Second, plugins...)
}

// callersOf reads who may use the API. The identity provider is a JSON object
// such as {"kind": "oidc", "settings": {...}}, opened with the plugin for its
// kind. The admin and user groups are JSON lists such as ["platform-team"].
// If the kind is empty, requests are not authenticated and the groups are
// ignored.
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

func driverConfig() (driver.Config, error) {
	var config driver.Config
	// The harnesses are a JSON object such as {"opencode": {"image":
	// "registry.example.com/harness-opencode:1.0", "resources": {"requests":
	// {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "4Gi"}}}}.
	encoded, err := env(harnesses)
	if err != nil {
		return config, err
	}
	if config.Models, err = address(modelsURL); err != nil {
		return config, err
	}
	if err := json.Unmarshal([]byte(encoded), &config.Harnesses); err != nil {
		return config, fmt.Errorf("%s: %w", harnesses, err)
	}
	for name, harness := range config.Harnesses {
		if harness.Image == "" {
			return config, fmt.Errorf("%s: harness %s has no image", harnesses, name)
		}
	}
	idle, err := env(sandboxIdle)
	if err != nil {
		return config, err
	}
	seconds, err := strconv.Atoi(idle)
	if err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or 0 to disable sleeping", sandboxIdle)
	}
	config.Idle = time.Duration(seconds) * time.Second
	retention, err := env(sandboxRetention)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(retention); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or 0 to keep sandboxes until their session ends", sandboxRetention)
	}
	config.Retention = time.Duration(seconds) * time.Second
	limit, err := env(maxConcurrentTurns)
	if err != nil {
		return config, err
	}
	if config.MaxConcurrentTurns, err = strconv.Atoi(limit); err != nil || config.MaxConcurrentTurns < 1 {
		return config, fmt.Errorf("%s must be a positive number", maxConcurrentTurns)
	}
	queued, err := env(maxQueuedTurns)
	if err != nil {
		return config, err
	}
	if config.MaxQueuedTurns, err = strconv.Atoi(queued); err != nil || config.MaxQueuedTurns < 0 {
		return config, fmt.Errorf("%s must be a number, or 0 for no limit", maxQueuedTurns)
	}
	wait, err := env(maxQueueWait)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(wait); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or 0 for no limit", maxQueueWait)
	}
	config.MaxQueueWait = time.Duration(seconds) * time.Second
	startup, err := env(maxStartup)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(startup); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or 0 for no limit", maxStartup)
	}
	config.MaxStartup = time.Duration(seconds) * time.Second
	longest, err := env(maxTurn)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(longest); err != nil || seconds < 1 {
		return config, fmt.Errorf("%s must be a positive number of seconds", maxTurn)
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
