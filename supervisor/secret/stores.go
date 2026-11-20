package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"

	"aviary/supervisor/store"
)

// timeout bounds each request to a store. It must stay below the Egress
// Proxy's authz timeout, since an authz check may wait for a store.
const timeout = 3 * time.Second

// Cache limits. A value is at most maxValue bytes. At most maxValues values
// are cached, taking at most maxBytes with their paths and keys, and the login
// sessions of at most maxStores stores. Beyond that, the least recently used
// entries are evicted.
const (
	maxValue  = 8 << 10
	maxValues = 10000
	maxBytes  = 16 << 20
	maxStores = 1000
)

// ErrSessionEnded is returned by Plugin.Read when the store no longer accepts
// the session. Stores then logs in again.
var ErrSessionEnded = errors.New("secret store session ended")

// host matches an allowed host: a host name, with or without a port.
var host = regexp.MustCompile(`^[^/@ *]+$`)

// Plugin reads from the secret stores of one kind. A kind is an API, which
// more than one product may serve. To support another kind, add its plugin to
// the build. A plugin keeps no state: every call gets the store's settings.
type Plugin interface {
	// Kind returns the name of the kind, which projects give when they
	// register a store.
	Kind() string
	// Address validates the settings of a store and returns its URL. The
	// error says what is wrong with the settings. Settings are not treated
	// as secret; the credential is passed to Login separately.
	Address(settings json.RawMessage) (string, error)
	// Login logs in to a store with its credential, sending its requests
	// through client. It returns a session, which only the plugin looks
	// into, and how long the session is valid. If good is not positive, the
	// session is used until Read returns ErrSessionEnded.
	Login(ctx context.Context, client *http.Client, settings json.RawMessage, credential string) (session any, good time.Duration, err error)
	// Read returns the value under key of the secret at path, using a
	// session from Login. It returns ErrSessionEnded if the store no longer
	// accepts the session. Plugins only read: Aviary never writes to the
	// store of a project.
	Read(ctx context.Context, client *http.Client, settings json.RawMessage, session any, path, key string) (string, error)
}

// Stores reads from the secret stores in which projects keep secrets outside
// Aviary. Since the Supervisor connects to the stores itself, it connects only
// to hosts that the installation allows.
type Stores struct {
	hosts   []string
	plugins map[string]Plugin
	client  *http.Client
	// Asked, if not nil, is called after every read from a store with the
	// store's kind, the duration and the error. Set it before the first
	// Read.
	Asked func(kind string, took time.Duration, err error)

	// lasts is how long values are cached.
	lasts    time.Duration
	values   *ttlcache.Cache[placed, string]
	sessions *ttlcache.Cache[registration, any]
	// reading and logging deduplicate concurrent reads of a value and
	// concurrent logins to a store.
	reading, logging singleflight.Group
	closed           chan struct{}
	closing          sync.Once
}

// registration identifies a store as of its last update, so that replacing a
// store invalidates what is cached for it.
type registration struct {
	id uuid.UUID
	at int64
}

func registered(from store.SecretStore) registration {
	return registration{id: from.ID, at: from.UpdatedAt.UnixNano()}
}

// placed identifies a value in a store.
type placed struct {
	registration
	path, key string
}

// NewStores returns Stores that allow the given hosts and serve the kinds of
// plugins. A host is "*" for any host, a host name for the default HTTPS and
// HTTP ports, or a host name with a port. Values read from a store are cached
// for remembered; 0 disables the cache. Call Close when the Stores are no
// longer needed.
func NewStores(hosts []string, remembered time.Duration, plugins ...Plugin) (*Stores, error) {
	for _, allowed := range hosts {
		if allowed != "*" && !host.MatchString(allowed) {
			return nil, fmt.Errorf(`allowed secret store host %q is neither "*" nor a host name with an optional port`, allowed)
		}
	}
	// Connect directly, never through a proxy from the environment.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	stores := &Stores{hosts: hosts, plugins: map[string]Plugin{}, lasts: remembered, closed: make(chan struct{}), client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// Redirects are not followed, since they could lead to a host that
		// is not allowed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	// Reads do not extend the lifetime of a cached value.
	stores.values = ttlcache.New(
		ttlcache.WithDisableTouchOnHit[placed, string](),
		ttlcache.WithCapacity[placed, string](maxValues),
		ttlcache.WithMaxCost(maxBytes, func(item ttlcache.CostItem[placed, string]) uint64 {
			return uint64(len(item.Key.path) + len(item.Key.key) + len(item.Value))
		}),
	)
	stores.sessions = ttlcache.New(
		ttlcache.WithDisableTouchOnHit[registration, any](),
		ttlcache.WithCapacity[registration, any](maxStores),
	)
	go stores.forget()
	for _, plugin := range plugins {
		stores.plugins[plugin.Kind()] = plugin
	}
	return stores, nil
}

// forget deletes expired entries periodically until the Stores are closed, so
// that expired values do not stay in memory.
func (s *Stores) forget() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			s.values.DeleteExpired()
			s.sessions.DeleteExpired()
		case <-s.closed:
			return
		}
	}
}

// Close stops the background cleanup. The Stores must not be used after
// Close.
func (s *Stores) Close() {
	s.closing.Do(func() { close(s.closed) })
}

// Kinds returns the kinds of stores that the Stores serve, sorted.
func (s *Stores) Kinds() []string {
	return slices.Sorted(maps.Keys(s.plugins))
}

// Check validates a store that a project registers: its kind must be known,
// its settings valid and its host allowed. It then logs in with credential,
// so that a store that does not work is reported now rather than in a turn.
func (s *Stores) Check(ctx context.Context, kind string, settings json.RawMessage, credential string) error {
	plugin, known := s.plugins[kind]
	if !known {
		return fmt.Errorf("kind must be one of: %s", strings.Join(s.Kinds(), ", "))
	}
	address, err := plugin.Address(settings)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if !s.allows(address) {
		return errors.New("settings: host is not in supervisor.secretStores.allowedHosts")
	}
	if _, _, err := plugin.Login(ctx, s.client, settings, credential); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	return nil
}

// Read returns the value under key of the secret at path in the store from.
// credential is the opened credential of the store, which Read logs in with.
//
// A value is cached for the configured duration and never used after it, not
// even when the store is unreachable, so that a value removed from a store
// stops being used. The cache is keyed by the last update of the store.
func (s *Stores) Read(ctx context.Context, from store.SecretStore, credential, path, key string) (string, error) {
	plugin, found := s.plugins[from.Kind]
	if !found {
		return "", fmt.Errorf("unknown secret store kind %q", from.Kind)
	}
	address, err := plugin.Address(from.Settings)
	if err != nil {
		return "", fmt.Errorf("store settings: %w", err)
	}
	if !s.allows(address) {
		return "", errors.New("store host is not in supervisor.secretStores.allowedHosts")
	}

	at := placed{registered(from), path, key}
	if kept := s.values.Get(at); kept != nil {
		return kept.Value(), nil
	}
	// The shared read is not canceled with the first caller, since other
	// callers may be waiting for it.
	asking := context.WithoutCancel(ctx)
	asked := s.reading.DoChan(fmt.Sprintf("%s %d %q %q", at.id, at.at, path, key), func() (any, error) {
		// Another caller may have cached the value meanwhile.
		if kept := s.values.Get(at); kept != nil {
			return kept.Value(), nil
		}
		began := time.Now()
		read, err := s.ask(asking, plugin, from, credential, path, key)
		if err == nil && len(read) > maxValue {
			err = fmt.Errorf("value exceeds the maximum size of %d bytes", maxValue)
		}
		if s.Asked != nil {
			s.Asked(from.Kind, time.Since(began), err)
		}
		if err != nil {
			return "", err
		}
		if s.lasts > 0 {
			s.values.Set(at, read, s.lasts)
		}
		return read, nil
	})
	select {
	case answer := <-asked:
		if answer.Err != nil {
			return "", answer.Err
		}
		return answer.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ask reads a value with the cached session, and logs in again once if the
// store has ended the session.
func (s *Stores) ask(ctx context.Context, plugin Plugin, from store.SecretStore, credential, path, key string) (string, error) {
	for again := false; ; again = true {
		session, err := s.session(ctx, plugin, from, credential)
		if err != nil {
			return "", err
		}
		read, err := plugin.Read(ctx, s.client, from.Settings, session, path, key)
		if errors.Is(err, ErrSessionEnded) {
			s.sessions.Delete(registered(from))
			if !again {
				continue
			}
			return "", errors.New("store rejected the session of a new login")
		}
		return read, err
	}
}

// session returns the cached login session of the store. Without one, it
// logs in, once for all concurrent callers, and caches the session for as
// long as the store says that it is valid.
func (s *Stores) session(ctx context.Context, plugin Plugin, from store.SecretStore, credential string) (any, error) {
	as := registered(from)
	if kept := s.sessions.Get(as); kept != nil {
		return kept.Value(), nil
	}
	session, err, _ := s.logging.Do(fmt.Sprintf("%s %d", as.id, as.at), func() (any, error) {
		if kept := s.sessions.Get(as); kept != nil {
			return kept.Value(), nil
		}
		session, good, err := plugin.Login(ctx, s.client, from.Settings, credential)
		if err != nil {
			return nil, fmt.Errorf("login failed: %w", err)
		}
		if good <= 0 {
			good = ttlcache.NoTTL
		}
		s.sessions.Set(as, session, good)
		return session, nil
	})
	return session, err
}

// allows reports whether a store at address is on an allowed host.
func (s *Stores) allows(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil {
		return false
	}
	usual := parsed.Port() == "" || parsed.Port() == "443" || parsed.Port() == "80"
	for _, allowed := range s.hosts {
		if allowed == "*" || strings.EqualFold(allowed, parsed.Host) || (usual && strings.EqualFold(allowed, parsed.Hostname())) {
			return true
		}
	}
	return false
}
