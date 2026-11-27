// Package app runs the Supervisor. It reads its settings from the
// environment and connects to the database, the event log, the secret stores
// and the identity provider through the plugins of the build. A build is a
// main package that calls Main with its plugins.
package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/rs/cors"
	credprovider "go.ate.dev/substrate/api/credprovider/v1"
	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"k8s.io/apimachinery/pkg/util/validation"

	"aviary/supervisor/events"
	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/attribution"
	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/objects"
	"aviary/supervisor/internal/outbound"
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
	allowedOrigins     = "AVIARY_ALLOWED_ORIGINS"
	harnesses          = "AVIARY_HARNESSES"
	runnerImage        = "AVIARY_RUNNER_IMAGE"
	maxQueuedTurns     = "AVIARY_MAX_QUEUED_TURNS"
	maxQueueWait       = "AVIARY_MAX_QUEUE_WAIT_SECONDS"
	maxStartup         = "AVIARY_MAX_STARTUP_SECONDS"
	maxToolOutput      = "AVIARY_MAX_TOOL_OUTPUT_BYTES"
	maxAttachmentBytes = "AVIARY_MAX_ATTACHMENT_BYTES"
	maxAttachments     = "AVIARY_MAX_ATTACHMENTS"
	maxFileBytes       = "AVIARY_MAX_FILE_BYTES"
	maxTurn            = "AVIARY_MAX_TURN_SECONDS"
	sandboxAtespace    = "AVIARY_SANDBOX_ATESPACE"
	sandboxPool        = "AVIARY_SANDBOX_POOL"
	sandboxSnapshot    = "AVIARY_SANDBOX_SNAPSHOT"
	sandboxHosts       = "AVIARY_SANDBOX_HOSTS"
	sandboxIdle        = "AVIARY_SANDBOX_IDLE_SECONDS"
	sandboxRetention   = "AVIARY_SANDBOX_RETENTION_SECONDS"
	substrateAPI       = "AVIARY_SUBSTRATE_API"
	substrateAuthority = "AVIARY_SUBSTRATE_AUTHORITY"
	substrateToken     = "AVIARY_SUBSTRATE_TOKEN_FILE"
	substrateCA        = "AVIARY_SUBSTRATE_CA_FILE"
	substrateRouter    = "AVIARY_SUBSTRATE_ROUTER"
	routerName         = "AVIARY_SUBSTRATE_ROUTER_NAME"
	substrateIdentity  = "AVIARY_SUBSTRATE_IDENTITY_FILE"
	credentialsCert    = "AVIARY_CREDENTIALS_CERT_FILE"
	credentialsClients = "AVIARY_CREDENTIALS_CLIENT_CA_FILE"
	egressGateway      = "AVIARY_SUBSTRATE_EGRESS_GATEWAY"
	modelsLocalhost    = "AVIARY_LLM_LOCALHOST"
	objectStorage      = "AVIARY_OBJECT_STORAGE"
	apiAddr            = ":8080"
	credentialsAddr    = ":50051"
	metricsAddr        = ":9092"
	// shutdownTimeout is how long in-flight API requests get to finish.
	shutdownTimeout = 10 * time.Second
	// identityPatience bounds connecting to the identity provider at startup.
	identityPatience = 30 * time.Second
	// keepaliveInterval is the interval and the timeout of the keepalive
	// pings of the credentials server.
	keepaliveInterval = 30 * time.Second
)

// handover is how long a stopping Supervisor keeps serving, since
// the cluster may still send it new requests. It is a variable for tests.
var handover = 5 * time.Second

type ports struct {
	api, credentials, metrics net.Listener
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
// it starts the Supervisors. With the argument "state-metrics", it instead
// serves the state metrics of the installation until ctx is done.
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
	case len(args) == 1 && args[0] == "state-metrics":
		return serveState(ctx, database, plugins)
	}
	return errors.New("usage: aviary-supervisor [migrate [<version>] | state-metrics]")
}

// serveState serves the state metrics, which need the database and the names
// of the harnesses.
func serveState(ctx context.Context, database string, plugins Plugins) error {
	configured, err := harnessSettings()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, database, plugins.Databases...)
	if err != nil {
		return err
	}
	defer st.Close()
	on, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		return err
	}
	return serveStateOn(ctx, on, st, slices.Sorted(maps.Keys(configured)))
}

// serveStateOn serves the state metrics on the listener until ctx is done.
func serveStateOn(ctx context.Context, on net.Listener, st store.Store, harnesses []string) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.NewState(st, harnesses).Handler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(on) }()
	slog.Info("listening", "metrics", on.Addr().String())

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	slog.Info("stopping")
	stopping, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return server.Shutdown(stopping)
}

// sandboxSettings reads the settings of the sandboxes and of the Substrate
// that runs them.
func sandboxSettings(harnesses map[string]sandbox.Harness) (sandbox.Settings, error) {
	// An installation without harnesses needs no runner.
	settings := sandbox.Settings{Harnesses: harnesses, Runner: os.Getenv(runnerImage)}
	var err error
	for name, value := range map[string]*string{
		sandboxAtespace: &settings.Atespace, sandboxSnapshot: &settings.Snapshot,
		substrateAPI: &settings.API, substrateAuthority: &settings.Authority, substrateToken: &settings.TokenFile, substrateCA: &settings.CAFile,
		substrateRouter: &settings.Router, routerName: &settings.RouterName, substrateIdentity: &settings.IdentityFile,
	} {
		if *value, err = env(name); err != nil {
			return settings, err
		}
	}
	pool, err := env(sandboxPool)
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(pool), &settings.Pool); err != nil {
		return settings, fmt.Errorf("%s must be a JSON object of labels: %w", sandboxPool, err)
	}
	output, err := env(maxToolOutput)
	if err != nil {
		return settings, err
	}
	if settings.MaxToolOutputBytes, err = strconv.Atoi(output); err != nil || settings.MaxToolOutputBytes < 0 {
		return settings, fmt.Errorf("%s must be a number of bytes, or 0 for no limit", maxToolOutput)
	}
	return settings, nil
}

// sandboxHostsOf reads the hosts that every sandbox may reach, a JSON list
// of URLs such as ["https://*", "https://*.example.com:8443"], whose paths
// are ignored.
func sandboxHostsOf() ([]sandbox.Host, error) {
	encoded, err := env(sandboxHosts)
	if err != nil {
		return nil, err
	}
	var addresses []string
	if err := json.Unmarshal([]byte(encoded), &addresses); err != nil {
		return nil, fmt.Errorf("%s: %w", sandboxHosts, err)
	}
	hosts := make([]sandbox.Host, 0, len(addresses))
	for _, address := range addresses {
		host, err := sandbox.HostOf(address)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sandboxHosts, err)
		}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

// credentialsTLS returns the TLS of the credentials server: its certificate,
// which the egress gateway trusts, and only the gateway as its client. The
// files are renewed, so they are read for each connection.
func credentialsTLS() (*tls.Config, error) {
	files := map[string]string{}
	for _, name := range []string{credentialsCert, credentialsClients, egressGateway} {
		value, err := env(name)
		if err != nil {
			return nil, err
		}
		files[name] = value
	}
	gateway := files[egressGateway]
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			certificate, err := tls.LoadX509KeyPair(files[credentialsCert], files[credentialsCert])
			if err != nil {
				return nil, err
			}
			encoded, err := os.ReadFile(files[credentialsClients])
			if err != nil {
				return nil, err
			}
			clients := x509.NewCertPool()
			if !clients.AppendCertsFromPEM(encoded) {
				return nil, fmt.Errorf("%s has no certificate", files[credentialsClients])
			}
			return &tls.Config{
				MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate},
				ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clients,
				VerifyPeerCertificate: func(_ [][]byte, chains [][]*x509.Certificate) error {
					for _, chain := range chains {
						for _, uri := range chain[0].URIs {
							if uri.String() == gateway {
								return nil
							}
						}
					}
					return fmt.Errorf("the client is not %s", gateway)
				},
			}, nil
		},
	}, nil
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
	origins, err := browserOrigins()
	if err != nil {
		return err
	}
	config, err := driverConfig(ctx)
	if err != nil {
		return err
	}
	settings, err := sandboxSettings(config.Harnesses)
	if err != nil {
		return err
	}
	config.Halt = settings.Snapshot == "memory"
	client, err := sandbox.New(settings)
	if err != nil {
		return err
	}
	defer client.Close()
	secured, err := credentialsTLS()
	if err != nil {
		return err
	}

	var on ports
	if on.api, err = net.Listen("tcp", apiAddr); err != nil {
		return err
	}
	if on.credentials, err = net.Listen("tcp", credentialsAddr); err != nil {
		return err
	}
	if on.metrics, err = net.Listen("tcp", metricsAddr); err != nil {
		return err
	}
	return serveOn(ctx, on, st, ev, client, config, keys, stores, callers, origins, settings.Atespace, secured, outbound.Client(config.Localhost))
}

// serveOn serves on the listeners until ctx is done. keys seal the stored
// secrets, stores read the secrets kept in secret stores, callers says who
// may use the API, origins are the origins whose pages may call it from a
// browser, and atespace holds the sandboxes. The secrets of providers and
// servers are served over TLS with secured, or without TLS if it is nil.
// reach asks providers and servers for their models and tools.
func serveOn(ctx context.Context, on ports, st store.Store, ev *events.Streams, sandboxes driver.Sandboxes, config driver.Config, keys *secret.Keys, stores *secret.Stores, callers api.Callers, origins []string, atespace string, secured *tls.Config, reach *http.Client) error {
	m := metrics.New(slices.Sorted(maps.Keys(config.Harnesses)), stores.Kinds())
	stores.Asked = m.SecretRead
	st = announcing{Store: st, events: ev}
	dr := driver.New(config, sandboxes, st, ev, m)
	apiServer := &http.Server{
		Handler:           crossOrigin(origins, api.New(ctx, st, ev, dr, keys, stores, callers, reach)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// The egress gateway asks for secrets on a port of its own. A peer that
	// disappears does not close its connections, so both ends send keepalive
	// pings: the server sends its own and permits those of the gateway.
	options := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: keepaliveInterval, Timeout: keepaliveInterval}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveInterval / 2,
			PermitWithoutStream: true,
		}),
	}
	if secured != nil {
		options = append(options, grpc.Creds(grpccredentials.NewTLS(secured)))
	}
	credentialsServer := grpc.NewServer(options...)
	credprovider.RegisterCredentialProviderServer(credentialsServer, credentials.New(&credentials.Resolver{Store: st, Keys: keys, Stores: stores}, atespace))
	// It also tells the gateway whom the connections and requests of a sandbox
	// are for.
	extprocv3.RegisterExternalProcessorServer(credentialsServer, attribution.New(st, atespace))
	// Metrics have a port of their own, so that they are not exposed with the
	// API. They are served until serveOn returns.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", m.Handler())
	metricsServer := &http.Server{Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}
	defer metricsServer.Close()
	failed := make(chan error, 3)
	go func() { failed <- apiServer.Serve(on.api) }()
	go func() { failed <- credentialsServer.Serve(on.credentials) }()
	go func() { failed <- metricsServer.Serve(on.metrics) }()
	go dr.Run(ctx)
	slog.Info("listening", "api", on.api.Addr().String(), "credentials", on.credentials.Addr().String(),
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
	credentialsServer.GracefulStop()
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

// localhostOfProviders reads the host that stands in for the local host of
// the URL of a model provider or an MCP server: a host name, which the
// Supervisor and the sandboxes reach. It is optional: without it nothing
// stands in, and sandboxes do not reach the local host.
func localhostOfProviders() (string, error) {
	host := os.Getenv(modelsLocalhost)
	if host == "" {
		return "", nil
	}
	if _, err := netip.ParseAddr(host); err == nil || host == "localhost" || len(validation.IsDNS1123Subdomain(host)) > 0 {
		return "", fmt.Errorf("%s must be a host name other than localhost, without a port", modelsLocalhost)
	}
	return host, nil
}

// browserOrigins reads the origins whose pages may call the API from a
// browser, a JSON list such as ["https://aviary.example.com"]. "*" is every
// origin, and a host may have one "*" for any part of it.
func browserOrigins() ([]string, error) {
	encoded, err := env(allowedOrigins)
	if err != nil {
		return nil, err
	}
	var allowed []string
	if err := json.Unmarshal([]byte(encoded), &allowed); err != nil {
		return nil, fmt.Errorf("%s: %w", allowedOrigins, err)
	}
	for _, origin := range allowed {
		if origin == "*" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%s: %q is not an origin, such as https://aviary.example.com", allowedOrigins, origin)
		}
	}
	return allowed, nil
}

// crossOrigin returns the API for browsers: pages of the origins may call
// it, and pages of other origins may not. Without origins it is the API as
// it is, which a browser lets only the pages of the API's own address call.
func crossOrigin(origins []string, api http.Handler) http.Handler {
	if len(origins) == 0 {
		return api
	}
	return cors.New(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
		AllowedHeaders: []string{"Authorization", "Content-Type", "Last-Event-ID"},
		// A page may read when to send a refused message again.
		ExposedHeaders: []string{"Retry-After"},
		// Seconds for which a browser remembers what it may send.
		MaxAge: 600,
	}).Handler(api)
}

// harnessSettings reads the harnesses, a JSON object such as {"opencode":
// {"image": "registry.example.com/harness-opencode@sha256:...", "resources":
// {"limits": {"cpu": "2", "memory": "4Gi"}}}}.
func harnessSettings() (map[string]sandbox.Harness, error) {
	encoded, err := env(harnesses)
	if err != nil {
		return nil, err
	}
	var configured map[string]sandbox.Harness
	if err := json.Unmarshal([]byte(encoded), &configured); err != nil {
		return nil, fmt.Errorf("%s: %w", harnesses, err)
	}
	for name, harness := range configured {
		if harness.Image == "" {
			return nil, fmt.Errorf("%s: harness %s has no image", harnesses, name)
		}
	}
	return configured, nil
}

func driverConfig(ctx context.Context) (driver.Config, error) {
	var config driver.Config
	var err error
	if config.Harnesses, err = harnessSettings(); err != nil {
		return config, err
	}
	if config.Hosts, err = sandboxHostsOf(); err != nil {
		return config, err
	}
	if config.Localhost, err = localhostOfProviders(); err != nil {
		return config, err
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
	// Without object storage, messages carry no files.
	if location := os.Getenv(objectStorage); location != "" {
		store, err := objects.Open(ctx, location)
		if err != nil {
			return config, fmt.Errorf("%s: %w", objectStorage, err)
		}
		config.Objects = store
	}
	retention, err := env(sandboxRetention)
	if err != nil {
		return config, err
	}
	if seconds, err = strconv.Atoi(retention); err != nil || seconds < 0 {
		return config, fmt.Errorf("%s must be a number of seconds, or 0 to keep sandboxes until their session ends", sandboxRetention)
	}
	config.Retention = time.Duration(seconds) * time.Second
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
	size, err := env(maxAttachmentBytes)
	if err != nil {
		return config, err
	}
	if config.MaxAttachmentBytes, err = strconv.ParseInt(size, 10, 64); err != nil || config.MaxAttachmentBytes < 1 {
		return config, fmt.Errorf("%s must be a positive number of bytes", maxAttachmentBytes)
	}
	files, err := env(maxAttachments)
	if err != nil {
		return config, err
	}
	if config.MaxAttachments, err = strconv.Atoi(files); err != nil || config.MaxAttachments < 0 {
		return config, fmt.Errorf("%s must be a number, or 0 to take no attachments", maxAttachments)
	}
	written, err := env(maxFileBytes)
	if err != nil {
		return config, err
	}
	if config.MaxFileBytes, err = strconv.ParseInt(written, 10, 64); err != nil || config.MaxFileBytes < 1 {
		return config, fmt.Errorf("%s must be a positive number of bytes", maxFileBytes)
	}
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
