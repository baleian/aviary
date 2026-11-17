package secret

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// shelf is a secret store of the tests, which is in the memory of the test.
// It takes the login of one credential, and a session of it is a number: the
// store takes the session that it gave last.
type shelf struct {
	mu     sync.Mutex
	values map[string]string
	// good is how long the store says that a session is good.
	good time.Duration
	// session is the session that the store takes.
	session int
	// down is what the store fails with, if it is down.
	down          error
	logins, reads int
}

// shelves is a plugin of the tests, which asks the one shelf.
type shelves struct{ *shelf }

func (shelves) Kind() string { return "shelf" }

func (shelves) Address(settings json.RawMessage) (string, error) {
	var store struct{ Address string }
	if err := json.Unmarshal(settings, &store); err != nil || store.Address == "" {
		return "", errors.New("address is required")
	}
	return store.Address, nil
}

func (s shelves) Login(_ context.Context, _ *http.Client, _ json.RawMessage, credential string) (any, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins++
	if s.down != nil {
		return nil, 0, s.down
	}
	if credential != "the-credential" {
		return nil, 0, errors.New("the store takes another credential")
	}
	s.session++
	return s.session, s.good, nil
}

func (s shelves) Read(_ context.Context, _ *http.Client, _ json.RawMessage, session any, path, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.down != nil {
		return "", s.down
	}
	if session != s.session {
		return "", ErrSessionEnded
	}
	value, found := s.values[path+" "+key]
	if !found {
		return "", errors.New("the store has no such value")
	}
	return value, nil
}

// set changes what the shelf holds or how it answers.
func (s *shelf) set(change func(*shelf)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s)
}

// asked returns how often the shelf was logged in to and read.
func (s *shelf) asked() (logins, reads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins, s.reads
}

// clock is the time of a test, which passes when the test says.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) pass(passed time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(passed)
}

// shelved returns the stores of an installation that allows them at
// store.test, with a shelf that holds one value, and the clock that they
// read.
func shelved(t *testing.T) (*Stores, *shelf, *clock) {
	t.Helper()
	held := &shelf{values: map[string]string{"llm/model api_key": "sk-the-first"}, good: time.Hour}
	stores, err := NewStores([]string{"store.test"}, shelves{held})
	if err != nil {
		t.Fatal(err)
	}
	clock := &clock{now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	stores.now = clock.time
	return stores, held, clock
}

// registered returns a secret store of a project as it was registered at a
// time.
func registered(at time.Time) store.SecretStore {
	return store.SecretStore{ID: uuid.New(), Kind: "shelf", Settings: json.RawMessage(`{"address": "https://store.test"}`), UpdatedAt: at}
}

// A value is read from its store and remembered for a minute. For that long
// the store is not asked again, and after it no value is used that the store
// did not give anew.
func TestReadRemembersForAMinute(t *testing.T) {
	t.Parallel()
	stores, held, clock := shelved(t)
	from := registered(clock.time())
	ctx := context.Background()
	// read reads the one value and tells how often the store was asked by
	// then.
	read := func() (string, error, int, int) {
		value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key")
		logins, reads := held.asked()
		return value, err, logins, reads
	}
	if value, err, logins, reads := read(); err != nil || value != "sk-the-first" || logins != 1 || reads != 1 {
		t.Fatalf("the value was read as %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	// Within the minute the store is not asked: not when its value has
	// changed, and not when it is down.
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-second" })
	clock.pass(59 * time.Second)
	if value, err, logins, reads := read(); err != nil || value != "sk-the-first" || logins != 1 || reads != 1 {
		t.Errorf("within the minute, the value was read as %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	held.set(func(s *shelf) { s.down = errors.New("the store was not reached") })
	if value, err, _, reads := read(); err != nil || value != "sk-the-first" || reads != 1 {
		t.Errorf("within the minute and with the store down, the value was read as %q after %d reads: %v", value, reads, err)
	}
	// After the minute the value is the store's to give, and a store that is
	// down gives none.
	clock.pass(time.Second)
	if value, err, _, reads := read(); err == nil || value != "" || reads != 2 || !strings.Contains(err.Error(), "was not reached") {
		t.Errorf("after the minute and with the store down, the value was read as %q after %d reads: %v", value, reads, err)
	}
	held.set(func(s *shelf) { s.down = nil })
	if value, err, logins, reads := read(); err != nil || value != "sk-the-second" || logins != 1 || reads != 3 {
		t.Errorf("after the minute, the value was read as %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	// A value that the store has no longer is gone within the minute.
	held.set(func(s *shelf) { delete(s.values, "llm/model api_key") })
	clock.pass(time.Minute)
	if value, err, _, _ := read(); err == nil || value != "" {
		t.Errorf("a value that was taken out of the store was read as %q: %v", value, err)
	}
}

// The Supervisor logs in to a store once and reads with that session until
// it ends: when the time that the store gave it is up, or when the store
// takes it no longer.
func TestReadLogsInWhenItsSessionHasEnded(t *testing.T) {
	t.Parallel()
	stores, held, clock := shelved(t)
	held.set(func(s *shelf) { s.values["llm/other api_key"], s.values["llm/third api_key"] = "sk-other", "sk-third" })
	from := registered(clock.time())
	ctx := context.Background()
	read := func(path string) (string, error, int) {
		value, err := stores.Read(ctx, from, "the-credential", path, "api_key")
		logins, _ := held.asked()
		return value, err, logins
	}
	if value, err, logins := read("llm/model"); err != nil || value != "sk-the-first" || logins != 1 {
		t.Fatalf("the value was read as %q after %d logins: %v", value, logins, err)
	}
	// Another value of the store is read with the same session.
	if value, err, logins := read("llm/other"); err != nil || value != "sk-other" || logins != 1 {
		t.Errorf("another value was read as %q after %d logins: %v", value, logins, err)
	}
	// The store ends the session before its time.
	held.set(func(s *shelf) { s.session++ })
	if value, err, logins := read("llm/third"); err != nil || value != "sk-third" || logins != 2 {
		t.Errorf("after the store ended the session, a value was read as %q after %d logins: %v", value, logins, err)
	}
	// The time of the session is up.
	clock.pass(time.Hour)
	if value, err, logins := read("llm/model"); err != nil || value != "sk-the-first" || logins != 3 {
		t.Errorf("after the time of the session, the value was read as %q after %d logins: %v", value, logins, err)
	}
	_, reads := held.asked()
	if reads != 5 {
		t.Errorf("the store was read %d times, not once for each value and once more with the session that had ended", reads)
	}

	// A store that takes no login gives nothing, and is asked again the next
	// time.
	clock.pass(time.Hour)
	if value, err := stores.Read(ctx, from, "another-credential", "llm/model", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), "does not take the login") {
		t.Errorf("with a credential that the store does not take, the value was read as %q: %v", value, err)
	}
	if value, err, _ := read("llm/model"); err != nil || value != "sk-the-first" {
		t.Errorf("after a login that the store refused, the value was read as %q: %v", value, err)
	}
}

// What is remembered of a store is of the store as it was registered last,
// and of that store alone.
func TestReadOfAStoreThatWasReplaced(t *testing.T) {
	t.Parallel()
	stores, held, clock := shelved(t)
	from := registered(clock.time())
	ctx := context.Background()
	if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
		t.Fatalf("the value was read as %q: %v", value, err)
	}
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-second" })
	// The store is registered anew: with another address, or another
	// credential.
	replaced := from
	replaced.UpdatedAt = from.UpdatedAt.Add(time.Second)
	if value, err := stores.Read(ctx, replaced, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-second" {
		t.Errorf("the value of the store that was replaced was read as %q: %v", value, err)
	}
	if logins, reads := held.asked(); logins != 2 || reads != 2 {
		t.Errorf("the store that was replaced was logged in to %d times and read %d times", logins, reads)
	}
	// Another store with the same place.
	other := registered(clock.time())
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-third" })
	if value, err := stores.Read(ctx, other, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-third" {
		t.Errorf("the value of another store was read as %q: %v", value, err)
	}
	if value, err := stores.Read(ctx, replaced, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-second" {
		t.Errorf("the value that is remembered of the first store was read as %q: %v", value, err)
	}
}

// A store is read only if the installation knows its kind and allows it
// where it is, as they are now.
func TestReadOfAStoreThatIsNotAllowed(t *testing.T) {
	t.Parallel()
	stores, held, clock := shelved(t)
	ctx := context.Background()
	elsewhere := registered(clock.time())
	elsewhere.Settings = json.RawMessage(`{"address": "https://other.test"}`)
	unknown := registered(clock.time())
	unknown.Kind = "another"
	unreadable := registered(clock.time())
	unreadable.Settings = json.RawMessage(`{}`)
	for why, from := range map[string]store.SecretStore{
		"supervisor.secretStores.allowedHosts": elsewhere,
		`the kind "another"`:                   unknown,
		"address is required":                  unreadable,
	} {
		if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), why) {
			t.Errorf("a store that is to be refused for %q was read as %q: %v", why, value, err)
		}
	}
	if logins, reads := held.asked(); logins != 0 || reads != 0 {
		t.Errorf("the store was logged in to %d times and read %d times", logins, reads)
	}
}

// Many requests for a value that is not remembered are one login and one
// read, and whoever counts the reads is told of that one.
func TestReadAsksAStoreOnceForManyAtOnce(t *testing.T) {
	t.Parallel()
	stores, held, clock := shelved(t)
	type ask struct {
		kind   string
		failed bool
	}
	var mu sync.Mutex
	var asks []ask
	stores.Asked = func(kind string, took time.Duration, err error) {
		mu.Lock()
		defer mu.Unlock()
		asks = append(asks, ask{kind, err != nil})
		if took < 0 {
			t.Errorf("the store took %s", took)
		}
	}
	from := registered(clock.time())
	ctx := context.Background()
	start := make(chan struct{})
	var readers sync.WaitGroup
	for range 20 {
		readers.Go(func() {
			<-start
			if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
				t.Errorf("the value was read as %q: %v", value, err)
			}
		})
	}
	close(start)
	readers.Wait()
	if logins, reads := held.asked(); logins != 1 || reads != 1 {
		t.Errorf("for 20 requests at once, the store was logged in to %d times and read %d times", logins, reads)
	}
	// A read that fails is counted too.
	if _, err := stores.Read(ctx, from, "the-credential", "llm/nothing", "api_key"); err == nil {
		t.Error("a value that the store has not was read")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asks) != 2 || asks[0] != (ask{"shelf", false}) || asks[1] != (ask{"shelf", true}) {
		t.Errorf("the reads were counted as %+v", asks)
	}

	// A reader that waits no longer for the store leaves.
	gone, leave := context.WithCancel(ctx)
	leave()
	kept := stores.known[registration{id: from.ID, at: from.UpdatedAt.UnixNano()}]
	kept.asking <- struct{}{}
	if _, err := stores.Read(gone, from, "the-credential", "llm/other", "api_key"); !errors.Is(err, context.Canceled) {
		t.Errorf("a reader that waits no longer was answered with: %v", err)
	}
	<-kept.asking
}
