// Package app is the Supervisor as a program: it reads its settings from the
// environment, connects to the database, the event log and the secret
// stores with the plugins of the build, and serves. A build of the
// Supervisor is a main package that calls Main with its plugins.
package app

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
	sandboxesURL       = "AVIARY_SANDBOXES_URL"
	sandboxesKey       = "AVIARY_SANDBOXES_API_KEY"
	templateStore      = "AVIARY_TEMPLATE_STORE"
	apiAddr            = ":8080"
	authzAddr          = ":9091"
	metricsAddr        = ":9092"
	// shutdownTimeout is how long the requests to the API get to finish.
	shutdownTimeout = 10 * time.Second
	// identityPatience is how long the identity provider may take to answer
	// when the Supervisor starts.
	identityPatience = 30 * time.Second
	// keepaliveInterval is how often the Supervisor asks the other end of an
	// idle connection whether it is still there, and how long it waits for
	// the answer. The Egress Proxy asks the same of it.
	keepaliveInterval = 30 * time.Second
)

// handover is how long a Supervisor that was told to stop goes on serving.
// For that long the cluster may still send it new requests. The tests set it
// lower.
var handover = 5 * time.Second

// ports are what a Supervisor listens on.
type ports struct {
	api, authz, metrics net.Listener
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
// version instead, forward or back. With the argument "templates" it has
// the templates built that the sandboxes of the sessions are made from,
// which a release does once too.
func Run(ctx context.Context, args []string, plugins Plugins) error {
	if len(args) == 1 && args[0] == "templates" {
		return bake(ctx)
	}
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
	return errors.New("usage: aviary-supervisor [migrate [<version>] | templates]")
}

// bake has the sandbox service build the template of every harness that
// has none, and waits for them.
func bake(ctx context.Context) error {
	client, err := sandboxes()
	if err != nil {
		return err
	}
	templates, err := templatesOf()
	if err != nil {
		return err
	}
	return client.Bake(ctx, templates)
}

// sandboxes reads where the sandbox service is, and the key that it is
// asked with.
func sandboxes() (*sandbox.Client, error) {
	address, err := env(sandboxesURL)
	if err != nil {
		return nil, err
	}
	key, err := env(sandboxesKey)
	if err != nil {
		return nil, err
	}
	return sandbox.New(address, key)
}

// templatesOf reads what the sandboxes of the sessions are made from. The
// harnesses are a JSON object such as {"opencode": {"image":
// "registry.example.com/harness-opencode:1.0", "sandbox": {"cpu": "1",
// "memory": "2Gi", "disk": "4Gi"}}}. A release has the templates built from
// what its Supervisors then find them by, so both read the same.
func templatesOf() (sandbox.Templates, error) {
	templates := sandbox.Templates{}
	var err error
	if templates.Store, err = env(templateStore); err != nil {
		return templates, err
	}
	encoded, err := env(harnesses)
	if err != nil {
		return templates, err
	}
	if err := json.Unmarshal([]byte(encoded), &templates.Harnesses); err != nil {
		return templates, fmt.Errorf("%s: %w", harnesses, err)
	}
	for name, harness := range templates.Harnesses {
		size := harness.Sandbox
		if harness.Image == "" || size.CPU == "" || size.Memory == "" || size.Disk == "" {
			return templates, fmt.Errorf("%s: the harness %s needs an image and the cpu, memory and disk of its sandboxes", harnesses, name)
		}
	}
	output, err := env(maxToolOutput)
	if err != nil {
		return templates, err
	}
	if templates.MaxToolOutputBytes, err = strconv.Atoi(output); err != nil || templates.MaxToolOutputBytes < 0 {
		return templates, fmt.Errorf("%s must be a number of bytes, or zero for no limit", maxToolOutput)
	}
	return templates, nil
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
	client, err := sandboxes()
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

// serveOn serves on the ports until ctx is done, with the turns in the
// database of the store and their sandboxes at the sandbox service of the
// client. It seals the secrets that it stores with the keys, and reads the
// others from the stores. The callers are who may use the API.
func serveOn(ctx context.Context, on ports, st store.Store, ev *events.Streams, client *sandbox.Client, config driver.Config, keys *secret.Keys, stores *secret.Stores, callers api.Callers) error {
	m := metrics.New()
	stores.Asked = m.SecretRead
	dr := driver.New(config, client, st, ev, m)
	apiServer := &http.Server{
		Handler:           api.New(st, ev, dr, keys, stores, callers, ctx.Done()),
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
	// The Egress Proxy asks on a port of its own.
	authzServer := grpc.NewServer(alive...)
	authv3.RegisterAuthorizationServer(authzServer, authz.New(st, keys, stores))
	// The metrics are on a port of their own, which the clients of the API
	// have no way to. They are served until the Supervisor has stopped.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", m.Handler())
	metricsServer := &http.Server{Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}
	defer metricsServer.Close()
	failed := make(chan error, 3)
	go func() { failed <- apiServer.Serve(on.api) }()
	go func() { failed <- authzServer.Serve(on.authz) }()
	go func() { failed <- metricsServer.Serve(on.metrics) }()
	go dr.Run(ctx)
	slog.Info("listening", "api", on.api.Addr().String(),
		"authz", on.authz.Addr().String(), "metrics", on.metrics.Addr().String())

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
	err := apiServer.Shutdown(stopping)
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
	var err error
	if config.Templates, err = templatesOf(); err != nil {
		return config, err
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
