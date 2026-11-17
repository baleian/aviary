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

	"aviary/supervisor/store"
)

// timeout is how long a store gets to answer. The Egress Proxy waits a
// little longer for the Supervisor, which may ask a store before it answers.
const timeout = 3 * time.Second

// remembered is how long the Supervisor uses a value that it read from a
// store before it reads the value again.
const remembered = time.Minute

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
	// now tells the time. The tests set it.
	now func() time.Time

	// mu guards what is remembered of the stores, but for their sessions.
	mu sync.Mutex
	// known is what is remembered of the stores that were read, each as it
	// was registered last.
	known map[registration]*known
}

// registration tells a secret store as it was registered at a time from
// other stores, and from itself as it was before.
type registration struct {
	id uuid.UUID
	at int64
}

// known is what the Supervisor remembers of a secret store: the session of
// its login, and the values that it read.
type known struct {
	// asking is held by the one that asks the store, to whom the session
	// is. A store is asked one thing at a time, so that many requests for a
	// value that is not remembered are one login and one read.
	asking  chan struct{}
	session any
	// ends is when the session ends, if the store said.
	ends   time.Time
	values map[place]value
}

// place is the place of a value in a store.
type place struct{ path, key string }

// value is a value as it was read, and until when it is used.
type value struct {
	value string
	until time.Time
}

// NewStores returns the stores of an installation that allows them at the
// hosts and asks them with the plugins. A host is written as for the Egress
// Proxy: "*" is every host, a name the host on the ports of HTTPS and HTTP,
// and a name with a port the host on that port.
func NewStores(hosts []string, plugins ...Plugin) (*Stores, error) {
	for _, allowed := range hosts {
		if allowed != "*" && !host.MatchString(allowed) {
			return nil, fmt.Errorf(`a secret store may be at "*" or at a host name, with a port if it is neither 443 nor 80, and %q is neither`, allowed)
		}
	}
	// The Supervisor reaches a store itself, and not through a proxy of its
	// environment.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	stores := &Stores{hosts: hosts, plugins: map[string]Plugin{}, now: time.Now, known: map[registration]*known{}, client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// A store that sends the Supervisor elsewhere is not followed: where
		// it leads may be a host that the installation does not allow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for _, plugin := range plugins {
		stores.plugins[plugin.Kind()] = plugin
	}
	return stores, nil
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
// The Supervisor remembers a value for a minute, and for that long it does
// not ask the store again. It uses no value for longer than that, also when
// the store cannot be asked: a value that the project took out of its store
// is gone from Aviary within the minute. What it remembers is of the store
// as it was last registered.
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

	at := place{path, key}
	kept, read, found := s.remembered(from, at)
	if found {
		return read, nil
	}
	select {
	case kept.asking <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-kept.asking }()
	// Another request may have asked the store for the value by now.
	if _, read, found := s.remembered(from, at); found {
		return read, nil
	}
	began := s.now()
	read, err = s.ask(ctx, plugin, from.Settings, credential, kept, at)
	if s.Asked != nil {
		s.Asked(from.Kind, s.now().Sub(began), err)
	}
	if err != nil {
		return "", err
	}
	until := s.now().Add(remembered)
	s.mu.Lock()
	kept.values[at] = value{value: read, until: until}
	s.mu.Unlock()
	// A value is not kept for longer than it is used.
	time.AfterFunc(remembered, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if held, found := kept.values[at]; found && held.until.Equal(until) {
			delete(kept.values, at)
		}
	})
	return read, nil
}

// remembered returns what is remembered of a store as it is registered now,
// and the value at the place, if it was read within the last minute.
func (s *Stores) remembered(from store.SecretStore, at place) (*known, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	as := registration{id: from.ID, at: from.UpdatedAt.UnixNano()}
	kept := s.known[as]
	if kept == nil {
		// What is remembered of the store as it was before is of no use.
		for other := range s.known {
			if other.id == from.ID {
				delete(s.known, other)
			}
		}
		kept = &known{asking: make(chan struct{}, 1), values: map[place]value{}}
		s.known[as] = kept
	}
	read, found := kept.values[at]
	return kept, read.value, found && s.now().Before(read.until)
}

// ask reads a value from a store, which it logs in to if it holds no
// session of the store that is still good. A store that takes its session
// no longer is logged in to once more. The caller is the one that asks the
// store.
func (s *Stores) ask(ctx context.Context, plugin Plugin, settings json.RawMessage, credential string, kept *known, at place) (string, error) {
	for again := false; ; again = true {
		if kept.session == nil || (!kept.ends.IsZero() && !s.now().Before(kept.ends)) {
			session, good, err := plugin.Login(ctx, s.client, settings, credential)
			if err != nil {
				kept.session = nil
				return "", fmt.Errorf("the store does not take the login: %w", err)
			}
			kept.session, kept.ends = session, time.Time{}
			if good > 0 {
				kept.ends = s.now().Add(good)
			}
		}
		read, err := plugin.Read(ctx, s.client, settings, kept.session, at.path, at.key)
		if errors.Is(err, ErrSessionEnded) {
			kept.session = nil
			if !again {
				continue
			}
			return "", errors.New("the store took the login and then refused the session of it")
		}
		return read, err
	}
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
