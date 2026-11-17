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
	"time"
)

// timeout is how long a store gets to answer. The Egress Proxy waits a
// little longer for the Supervisor, which may ask a store before it answers.
const timeout = 3 * time.Second

// host is the form of a host that an installation allows: a host name, with
// a port or without.
var host = regexp.MustCompile(`^[^/@ *]+$`)

// Driver asks the secret stores of one kind. A kind is a way in which a
// store is asked, whatever product the store is. The Supervisor knows the
// kinds that it was built with: a store of another kind is served by
// writing a driver for it and adding the driver where the Supervisor
// starts. A driver keeps nothing of a store. It is told the settings of the
// store with every request.
type Driver interface {
	// Kind is the name of the kind, which a project gives with a store.
	Kind() string
	// Address reads the settings of a store, as a project gave them, and
	// returns where the store is. It tells what is wrong with the settings,
	// if anything is. Nothing in the settings is secret: what a store is
	// logged in to with is its credential.
	Address(settings json.RawMessage) (string, error)
	// Login logs in to a store with its credential. It returns what the
	// store is read with from then on, into which only the driver looks,
	// and how long that is good. The driver sends its requests through the
	// client.
	Login(ctx context.Context, client *http.Client, settings json.RawMessage, credential string) (session any, good time.Duration, err error)
}

// Stores are the secret stores of the projects: where a project keeps the
// secrets that it does not give to Aviary to store. An installation says at
// which hosts a store may be, since the Supervisor itself connects to it.
type Stores struct {
	hosts   []string
	drivers map[string]Driver
	client  *http.Client
}

// NewStores returns the stores of an installation that allows them at the
// hosts and asks them with the drivers. A host is written as for the Egress
// Proxy: "*" is every host, a name the host on the ports of HTTPS and HTTP,
// and a name with a port the host on that port.
func NewStores(hosts []string, drivers ...Driver) (*Stores, error) {
	for _, allowed := range hosts {
		if allowed != "*" && !host.MatchString(allowed) {
			return nil, fmt.Errorf(`a secret store may be at "*" or at a host name, with a port if it is neither 443 nor 80, and %q is neither`, allowed)
		}
	}
	// The Supervisor reaches a store itself, and not through a proxy of its
	// environment.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	stores := &Stores{hosts: hosts, drivers: map[string]Driver{}, client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// A store that sends the Supervisor elsewhere is not followed: where
		// it leads may be a host that the installation does not allow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for _, driver := range drivers {
		stores.drivers[driver.Kind()] = driver
	}
	return stores, nil
}

// Check tells what is wrong with a store that a project gives, if anything
// is. Its kind is to be one that the installation knows, its settings as
// the kind has them and its host one that the installation allows. The
// store is then tried, so that what does not work is found now and not in a
// turn: the Supervisor logs in to it with the credential.
func (s *Stores) Check(ctx context.Context, kind string, settings json.RawMessage, credential string) error {
	driver, known := s.drivers[kind]
	if !known {
		kinds := slices.Sorted(maps.Keys(s.drivers))
		return fmt.Errorf("kind is not a kind of secret store that this installation knows, which are: %s", strings.Join(kinds, ", "))
	}
	address, err := driver.Address(settings)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if !s.allows(address) {
		return errors.New("settings name a host that the installation allows no secret store at: supervisor.secretStores.allowedHosts lists them")
	}
	if _, _, err := driver.Login(ctx, s.client, settings, credential); err != nil {
		return fmt.Errorf("the store does not take the login: %w", err)
	}
	return nil
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
