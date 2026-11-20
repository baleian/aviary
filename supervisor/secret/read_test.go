package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// shelf is an in-memory secret store. It takes one credential, and its
// sessions are numbers, of which only the latest is valid.
type shelf struct {
	mu     sync.Mutex
	values map[string]string
	// every, if not empty, is the value at every place without one of its
	// own.
	every string
	// good is the session lifetime that Login returns.
	good time.Duration
	// session is the only valid session.
	session int
	// down, if not nil, is the error of every call.
	down error
	// held, if not nil, blocks reads until it is closed.
	held          chan struct{}
	logins, reads int
}

// shelves is a plugin backed by a single shelf.
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
	held := s.held
	s.mu.Unlock()
	if held != nil {
		<-held
	}
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
	switch {
	case found:
		return value, nil
	case s.every != "":
		return s.every, nil
	}
	return "", errors.New("the store has no such value")
}

// set applies change under the lock.
func (s *shelf) set(change func(*shelf)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s)
}

// asked returns the number of logins and reads.
func (s *shelf) asked() (logins, reads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins, s.reads
}

// lasts is how long these tests cache values: long enough to read twice, and
// short enough to wait out.
const lasts = time.Second

// shelved returns Stores that allow store.test and cache values for
// remembered, and a shelf that holds one value.
func shelved(t *testing.T, remembered time.Duration) (*Stores, *shelf) {
	t.Helper()
	held := &shelf{values: map[string]string{"llm/model api_key": "sk-the-first"}, good: time.Hour}
	stores, err := NewStores([]string{"store.test"}, remembered, shelves{held})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stores.Close)
	return stores, held
}

// aStore returns a newly registered store.
func aStore() store.SecretStore {
	return store.SecretStore{ID: uuid.New(), Kind: "shelf", Settings: json.RawMessage(`{"address": "https://store.test"}`), UpdatedAt: time.Now()}
}

// A value is cached for a while, during which the store is not asked again.
// After that, only a fresh read from the store is used.
func TestReadRemembersForAWhile(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	from := aStore()
	ctx := context.Background()
	// read reads the value and returns the numbers of logins and reads.
	read := func() (string, error, int, int) {
		value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key")
		logins, reads := held.asked()
		return value, err, logins, reads
	}
	if value, err, logins, reads := read(); err != nil || value != "sk-the-first" || logins != 1 || reads != 1 {
		t.Fatalf("Read = %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	// While cached, the value is used even if the store has changed or is
	// down.
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-second" })
	if value, err, logins, reads := read(); err != nil || value != "sk-the-first" || logins != 1 || reads != 1 {
		t.Errorf("second Read = %q after %d logins and %d reads: %v; want the cached value", value, logins, reads, err)
	}
	held.set(func(s *shelf) { s.down = errors.New("the store was not reached") })
	if value, err, _, reads := read(); err != nil || value != "sk-the-first" || reads != 1 {
		t.Errorf("Read with the store down = %q after %d reads: %v; want the cached value", value, reads, err)
	}
	// Once it expires, a store that is down yields an error, not the stale
	// value.
	time.Sleep(lasts + 100*time.Millisecond)
	if value, err, _, reads := read(); err == nil || value != "" || reads != 2 || !strings.Contains(err.Error(), "was not reached") {
		t.Errorf("Read after expiry with the store down = %q after %d reads: %v; want the store's error", value, reads, err)
	}
	held.set(func(s *shelf) { s.down = nil })
	if value, err, logins, reads := read(); err != nil || value != "sk-the-second" || logins != 1 || reads != 3 {
		t.Errorf("Read after expiry = %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	// A value deleted from the store is gone once the cached one expires.
	held.set(func(s *shelf) { delete(s.values, "llm/model api_key") })
	time.Sleep(lasts + 100*time.Millisecond)
	if value, err, _, _ := read(); err == nil || value != "" {
		t.Errorf("Read of a deleted value = %q, %v; want an error", value, err)
	}
}

// Frequent reads do not extend the lifetime of a cached value.
func TestReadAgainOfAValueThatIsAskedForAllTheTime(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	from := aStore()
	for until := time.Now().Add(lasts + lasts/2); time.Now().Before(until); time.Sleep(50 * time.Millisecond) {
		if _, err := stores.Read(context.Background(), from, "the-credential", "llm/model", "api_key"); err != nil {
			t.Fatal(err)
		}
	}
	if _, reads := held.asked(); reads < 2 {
		t.Errorf("store reads = %d, want at least 2", reads)
	}
}

// Expired values are evicted even if nobody reads them again.
func TestReadForgetsWhatItUsesNoLonger(t *testing.T) {
	t.Parallel()
	stores, _ := shelved(t, 200*time.Millisecond)
	if _, err := stores.Read(context.Background(), aStore(), "the-credential", "llm/model", "api_key"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); stores.values.Metrics().Evictions == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("expired value was not evicted")
		}
	}
}

// With a cache duration of 0, every read goes to the store.
func TestReadWithNothingRemembered(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, 0)
	from := aStore()
	for expected, value := range []string{"sk-the-first", "sk-the-second", "sk-the-third"} {
		held.set(func(s *shelf) { s.values["llm/model api_key"] = value })
		read, err := stores.Read(context.Background(), from, "the-credential", "llm/model", "api_key")
		if _, reads := held.asked(); err != nil || read != value || reads != expected+1 {
			t.Errorf("uncached Read = %q after %d reads: %v", read, reads, err)
		}
	}
	if logins, _ := held.asked(); logins != 1 || stores.values.Len() != 0 {
		t.Errorf("without a cache: %d logins and %d cached values, want 1 and 0", logins, stores.values.Len())
	}
}

// A login session is reused until it expires or the store ends it.
func TestReadLogsInWhenItsSessionHasEnded(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	held.set(func(s *shelf) {
		s.values["llm/other api_key"], s.values["llm/third api_key"], s.values["llm/fourth api_key"] = "sk-other", "sk-third", "sk-fourth"
		s.good = lasts
	})
	from := aStore()
	ctx := context.Background()
	read := func(path string) (string, error, int) {
		value, err := stores.Read(ctx, from, "the-credential", path, "api_key")
		logins, _ := held.asked()
		return value, err, logins
	}
	if value, err, logins := read("llm/model"); err != nil || value != "sk-the-first" || logins != 1 {
		t.Fatalf("Read = %q after %d logins: %v", value, logins, err)
	}
	// Another value is read with the same session.
	if value, err, logins := read("llm/other"); err != nil || value != "sk-other" || logins != 1 {
		t.Errorf("Read of another value = %q after %d logins: %v", value, logins, err)
	}
	// The store ends the session early.
	held.set(func(s *shelf) { s.session++ })
	if value, err, logins := read("llm/third"); err != nil || value != "sk-third" || logins != 2 {
		t.Errorf("Read after the store ended the session = %q after %d logins: %v", value, logins, err)
	}
	if _, reads := held.asked(); reads != 4 {
		t.Errorf("store reads = %d, want 4", reads)
	}
	// The session expires.
	time.Sleep(lasts + 100*time.Millisecond)
	if value, err, logins := read("llm/fourth"); err != nil || value != "sk-fourth" || logins != 3 {
		t.Errorf("Read after the session expired = %q after %d logins: %v", value, logins, err)
	}

	// A failed login is not cached.
	other := aStore()
	if value, err := stores.Read(ctx, other, "another-credential", "llm/model", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), "login failed") {
		t.Errorf("Read with a wrong credential = %q, %v; want a login error", value, err)
	}
	if value, err := stores.Read(ctx, other, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
		t.Errorf("Read after a failed login = %q, %v", value, err)
	}
}

// The cache is per store and per registration: replacing a store invalidates
// its cached values and session.
func TestReadOfAStoreThatWasReplaced(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	from := aStore()
	ctx := context.Background()
	if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
		t.Fatalf("Read = %q, %v", value, err)
	}
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-second" })
	// Replace the store, as with another address or credential.
	replaced := from
	replaced.UpdatedAt = from.UpdatedAt.Add(time.Second)
	if value, err := stores.Read(ctx, replaced, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-second" {
		t.Errorf("Read of the replaced store = %q, %v", value, err)
	}
	if logins, reads := held.asked(); logins != 2 || reads != 2 {
		t.Errorf("replaced store: %d logins and %d reads, want 2 and 2", logins, reads)
	}
	// Another store with the same path and key.
	other := aStore()
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-third" })
	if value, err := stores.Read(ctx, other, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-third" {
		t.Errorf("Read of another store = %q, %v", value, err)
	}
	if value, err := stores.Read(ctx, replaced, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-second" {
		t.Errorf("cached Read of the replaced store = %q, %v", value, err)
	}
}

// A store is read only if its kind is known and its host is allowed at the
// time of the read.
func TestReadOfAStoreThatIsNotAllowed(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	ctx := context.Background()
	elsewhere := aStore()
	elsewhere.Settings = json.RawMessage(`{"address": "https://other.test"}`)
	unknown := aStore()
	unknown.Kind = "another"
	unreadable := aStore()
	unreadable.Settings = json.RawMessage(`{}`)
	for why, from := range map[string]store.SecretStore{
		"supervisor.secretStores.allowedHosts": elsewhere,
		`kind "another"`:                       unknown,
		"address is required":                  unreadable,
	} {
		if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: Read = %q, %v; want an error containing it", why, value, err)
		}
	}
	if logins, reads := held.asked(); logins != 0 || reads != 0 {
		t.Errorf("%d logins and %d reads, want none", logins, reads)
	}
}

// Concurrent reads of a value that is not cached share one login and one
// read, which Asked reports once.
func TestReadAsksAStoreOnceForManyAtOnce(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
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
			t.Errorf("Asked called with negative duration %s", took)
		}
	}
	from := aStore()
	ctx := context.Background()
	// The store answers once all readers have asked.
	waiting := make(chan struct{})
	held.set(func(s *shelf) { s.held = waiting })
	var readers sync.WaitGroup
	for range 20 {
		readers.Go(func() {
			if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
				t.Errorf("Read = %q, %v", value, err)
			}
		})
	}
	// A canceled reader returns at once without affecting the others.
	gone, leave := context.WithCancel(ctx)
	leave()
	if _, err := stores.Read(gone, from, "the-credential", "llm/model", "api_key"); !errors.Is(err, context.Canceled) {
		t.Errorf("Read with a canceled context = %v, want context.Canceled", err)
	}
	time.Sleep(100 * time.Millisecond)
	close(waiting)
	readers.Wait()
	held.set(func(s *shelf) { s.held = nil })
	if logins, reads := held.asked(); logins != 1 || reads != 1 {
		t.Errorf("20 concurrent reads: %d logins and %d reads, want 1 and 1", logins, reads)
	}
	// Failed reads are reported too.
	if _, err := stores.Read(ctx, from, "the-credential", "llm/nothing", "api_key"); err == nil {
		t.Error("Read of a missing value succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asks) != 2 || asks[0] != (ask{"shelf", false}) || asks[1] != (ask{"shelf", true}) {
		t.Errorf("Asked calls = %+v", asks)
	}
}

// The cache limits are kept by evicting the least recently used entries.
func TestReadRemembersNoMoreThanItMay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("values", func(t *testing.T) {
		t.Parallel()
		stores, held := shelved(t, time.Hour)
		held.set(func(s *shelf) { s.every = "sk-of-every-place" })
		from := aStore()
		for i := range maxValues + 50 {
			if _, err := stores.Read(ctx, from, "the-credential", fmt.Sprintf("llm/%d", i), "api_key"); err != nil {
				t.Fatal(err)
			}
		}
		if kept := stores.values.Len(); kept != maxValues {
			t.Errorf("cached values = %d, want %d", kept, maxValues)
		}
		// The first value was evicted and the last was not.
		_, before := held.asked()
		stores.Read(ctx, from, "the-credential", fmt.Sprintf("llm/%d", maxValues+49), "api_key")
		if _, reads := held.asked(); reads != before {
			t.Error("last value read was evicted")
		}
		stores.Read(ctx, from, "the-credential", "llm/0", "api_key")
		if _, reads := held.asked(); reads != before+1 {
			t.Error("first value read was not evicted")
		}
	})

	t.Run("bytes", func(t *testing.T) {
		t.Parallel()
		stores, held := shelved(t, time.Hour)
		held.set(func(s *shelf) { s.every = strings.Repeat("k", maxValue) })
		from := aStore()
		most := maxBytes / maxValue
		for i := range most + 50 {
			if _, err := stores.Read(ctx, from, "the-credential", fmt.Sprintf("llm/%d", i), "api_key"); err != nil {
				t.Fatal(err)
			}
		}
		if kept := stores.values.Len(); kept > most || kept < most-50 {
			t.Errorf("cached %d values of %d bytes, want about %d bytes in total", kept, maxValue, maxBytes)
		}
		// A value longer than maxValue is refused.
		held.set(func(s *shelf) { s.every = strings.Repeat("k", maxValue+1) })
		if value, err := stores.Read(ctx, from, "the-credential", "llm/long", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), "exceeds the maximum size") {
			t.Errorf("Read of an oversized value = %d bytes, %v; want an error", len(value), err)
		}
	})

	t.Run("stores", func(t *testing.T) {
		t.Parallel()
		stores, held := shelved(t, 0)
		first := aStore()
		if _, err := stores.Read(ctx, first, "the-credential", "llm/model", "api_key"); err != nil {
			t.Fatal(err)
		}
		for range maxStores {
			if _, err := stores.Read(ctx, aStore(), "the-credential", "llm/model", "api_key"); err != nil {
				t.Fatal(err)
			}
		}
		if kept := stores.sessions.Len(); kept != maxStores {
			t.Errorf("cached sessions = %d, want %d", kept, maxStores)
		}
		// The session of the first store was evicted.
		before, _ := held.asked()
		if _, err := stores.Read(ctx, first, "the-credential", "llm/model", "api_key"); err != nil {
			t.Fatal(err)
		}
		if logins, _ := held.asked(); logins != before+1 {
			t.Errorf("first store: %d more logins, want 1", logins-before)
		}
	})
}
