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

// timeout is how long a store gets to answer. The Egress Proxy waits a
// little longer for the Supervisor, which may ask a store before it answers.
const timeout = 3 * time.Second

// What a Supervisor remembers of the secret stores is no more than this,
// however many stores and values it is asked for. A value is no longer than
// maxValue bytes. No more than maxValues of them are remembered, which
// together with their places are no more than maxBytes, and of no more than
// maxStores stores the session of a login. Beyond that the Supervisor
// forgets what it used longest ago.
const (
	maxValue  = 8 << 10
	maxValues = 10000
	maxBytes  = 16 << 20
	maxStores = 1000
)

// ErrSessionEnded is what a plugin answers a read with when the store no
// longer takes the session that it logged in for. The store is then logged
// in to again.
var ErrSessionEnded = errors.New("the store no longer takes the session")

// host is the form of a host that an installation allows: a host name, with
// a port or without.
var host = regexp.MustCompile(`^[^/@ *]+$`)

// Plugin asks the secret stores of one kind. A kind is a way in which a
// store is asked, whatever product the store is. The Supervisor knows the
// kinds that it was built with: a store of another kind is served by
// writing a plugin for it and adding the plugin to the build. A plugin
// keeps nothing of a store. It is told the settings of the store with every
// request.
type Plugin interface {
	// Kind is the name of the kind, which a project gives with a store.
	Kind() string
	// Address reads the settings of a store, as a project gave them, and
	// returns where the store is. It tells what is wrong with the settings,
	// if anything is. Nothing in the settings is secret: what a store is
	// logged in to with is its credential.
	Address(settings json.RawMessage) (string, error)
	// Login logs in to a store with its credential. It returns what the
	// store is read with from then on, into which only the plugin looks,
	// and how long that is good. The plugin sends its requests through the
	// client. A session that is good for no known time is good until the
	// store says that it has ended.
	Login(ctx context.Context, client *http.Client, settings json.RawMessage, credential string) (session any, good time.Duration, err error)
	// Read reads from a store, with the session of a login, the value that
	// the secret at the path holds under the key. It returns ErrSessionEnded
	// if the store no longer takes the session. A plugin only reads: Aviary
	// writes nothing to the store of a project.
	Read(ctx context.Context, client *http.Client, settings json.RawMessage, session any, path, key string) (string, error)
}

// Stores are the secret stores of the projects: where a project keeps the
// secrets that it does not give to Aviary to store. An installation says at
// which hosts a store may be, since the Supervisor itself connects to it.
type Stores struct {
	hosts   []string
	plugins map[string]Plugin
	client  *http.Client
	// Asked, if it is set, is told of every time that a store was asked for
	// a value: the kind of the store, how long the store took, and what went
	// wrong, if anything did. It is set before the stores are read.
	Asked func(kind string, took time.Duration, err error)

	// lasts is for how long a value is remembered.
	lasts time.Duration
	// values are the values that were read, each until it is to be read
	// again, and sessions the sessions of the logins, each until it ends.
	// Neither holds more than it may: it forgets what was used longest ago.
	values   *ttlcache.Cache[placed, string]
	sessions *ttlcache.Cache[registration, any]
	// reading and logging hold what is under way, so that many requests at
	// once for the same value, or for a login to the same store, are one.
	reading, logging singleflight.Group
	// closed is closed when the stores are, and closing sees to it once.
	closed  chan struct{}
	closing sync.Once
}

// registration tells a secret store as it was registered at a time from
// other stores, and from itself as it was before: what is remembered of a
// store is of the store as it was registered last.
type registration struct {
	id uuid.UUID
	at int64
}

func registered(from store.SecretStore) registration {
	return registration{id: from.ID, at: from.UpdatedAt.UnixNano()}
}

// placed is the place of a value in a store.
type placed struct {
	registration
	path, key string
}

// NewStores returns the stores of an installation that allows them at the
// hosts and asks them with the plugins. A host is written as for the Egress
// Proxy: "*" is every host, a name the host on the ports of HTTPS and HTTP,
// and a name with a port the host on that port. A value that is read from a
// store is remembered for so long, and not at all with zero: the store is
// then read every time. The stores are closed when they are of no more use.
func NewStores(hosts []string, remembered time.Duration, plugins ...Plugin) (*Stores, error) {
	for _, allowed := range hosts {
		if allowed != "*" && !host.MatchString(allowed) {
			return nil, fmt.Errorf(`a secret store may be at "*" or at a host name, with a port if it is neither 443 nor 80, and %q is neither`, allowed)
		}
	}
	// The Supervisor reaches a store itself, and not through a proxy of its
	// environment.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	stores := &Stores{hosts: hosts, plugins: map[string]Plugin{}, lasts: remembered, closed: make(chan struct{}), client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// A store that sends the Supervisor elsewhere is not followed: where
		// it leads may be a host that the installation does not allow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	// A value that is read again does not stay for longer.
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

// forget takes what is no longer used out of the memory, until the stores
// are closed: a value is not kept for longer than it is used.
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

// Close ends what the stores do by themselves. They are not read after it.
func (s *Stores) Close() {
	s.closing.Do(func() { close(s.closed) })
}

// Check tells what is wrong with a store that a project gives, if anything
// is. Its kind is to be one that the installation knows, its settings as
// the kind has them and its host one that the installation allows. The
// store is then tried, so that what does not work is found now and not in a
// turn: the Supervisor logs in to it with the credential.
func (s *Stores) Check(ctx context.Context, kind string, settings json.RawMessage, credential string) error {
	plugin, known := s.plugins[kind]
	if !known {
		kinds := slices.Sorted(maps.Keys(s.plugins))
		return fmt.Errorf("kind is not a kind of secret store that this installation knows, which are: %s", strings.Join(kinds, ", "))
	}
	address, err := plugin.Address(settings)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if !s.allows(address) {
		return errors.New("settings name a host that the installation allows no secret store at: supervisor.secretStores.allowedHosts lists them")
	}
	if _, _, err := plugin.Login(ctx, s.client, settings, credential); err != nil {
		return fmt.Errorf("the store does not take the login: %w", err)
	}
	return nil
}

// Read returns the value that the secret at the path holds under the key, in
// a secret store of a project. The credential is what the store is logged
// in to with, opened.
//
// The Supervisor remembers a value for as long as the installation says,
// and for that long it does not ask the store again. It uses no value for
// longer than that, also when the store cannot be asked: a value that the
// project took out of its store is gone from Aviary by then. What it
// remembers is of the store as it was last registered.
func (s *Stores) Read(ctx context.Context, from store.SecretStore, credential, path, key string) (string, error) {
	plugin, found := s.plugins[from.Kind]
	if !found {
		return "", fmt.Errorf("the store is of the kind %q, which this installation does not know", from.Kind)
	}
	address, err := plugin.Address(from.Settings)
	if err != nil {
		return "", fmt.Errorf("the settings of the store: %w", err)
	}
	if !s.allows(address) {
		return "", errors.New("the installation allows no secret store at the host of the store: supervisor.secretStores.allowedHosts lists them")
	}

	at := placed{registered(from), path, key}
	if kept := s.values.Get(at); kept != nil {
		return kept.Value(), nil
	}
	// The store is asked for as long as it may take, also if the request
	// that asks first is gone by then: others may wait for the same.
	asking := context.WithoutCancel(ctx)
	asked := s.reading.DoChan(fmt.Sprintf("%s %d %q %q", at.id, at.at, path, key), func() (any, error) {
		// Another request may have asked the store for the value by now.
		if kept := s.values.Get(at); kept != nil {
			return kept.Value(), nil
		}
		began := time.Now()
		read, err := s.ask(asking, plugin, from, credential, path, key)
		if err == nil && len(read) > maxValue {
			err = fmt.Errorf("the value is longer than a secret may be, which is %d bytes", maxValue)
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

// ask reads a value from a store, with the session of a login to it. A
// store that takes the session no longer is logged in to once more.
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
			return "", errors.New("the store took the login and then refused the session of it")
		}
		return read, err
	}
}

// session returns the session of a login to a store. The Supervisor logs in
// if it holds none that is still good, once for all that ask at that
// moment, and holds the session for as long as the store says that it is
// good.
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
			return nil, fmt.Errorf("the store does not take the login: %w", err)
		}
		if good <= 0 {
			good = ttlcache.NoTTL
		}
		s.sessions.Set(as, session, good)
		return session, nil
	})
	return session, err
}

// allows tells whether the installation allows a store at the address.
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
