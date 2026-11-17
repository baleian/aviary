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

// shelf is a secret store of the tests, which is in the memory of the test.
// It takes the login of one credential, and a session of it is a number: the
// store takes the session that it gave last.
type shelf struct {
	mu     sync.Mutex
	values map[string]string
	// every is what the shelf holds at every place that it has no value
	// for, if it holds something everywhere.
	every string
	// good is how long the store says that a session is good.
	good time.Duration
	// session is the session that the store takes.
	session int
	// down is what the store fails with, if it is down.
	down error
	// held, if it is there, keeps a read waiting until it is closed.
	held          chan struct{}
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

// lasts is for how long the stores of these tests remember a value: long
// enough for a test to read twice, and short enough to wait for.
const lasts = time.Second

// shelved returns the stores of an installation that allows them at
// store.test and remembers their values for so long, and a shelf that holds
// one value.
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

// aStore returns a secret store of a project as it was registered now.
func aStore() store.SecretStore {
	return store.SecretStore{ID: uuid.New(), Kind: "shelf", Settings: json.RawMessage(`{"address": "https://store.test"}`), UpdatedAt: time.Now()}
}

// A value is read from its store and remembered for a while. For that long
// the store is not asked again, and after it no value is used that the store
// did not give anew.
func TestReadRemembersForAWhile(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	from := aStore()
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
	// For that while the store is not asked: not when its value has changed,
	// and not when it is down.
	held.set(func(s *shelf) { s.values["llm/model api_key"] = "sk-the-second" })
	if value, err, logins, reads := read(); err != nil || value != "sk-the-first" || logins != 1 || reads != 1 {
		t.Errorf("at once again, the value was read as %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	held.set(func(s *shelf) { s.down = errors.New("the store was not reached") })
	if value, err, _, reads := read(); err != nil || value != "sk-the-first" || reads != 1 {
		t.Errorf("at once again and with the store down, the value was read as %q after %d reads: %v", value, reads, err)
	}
	// After it the value is the store's to give, and a store that is down
	// gives none.
	time.Sleep(lasts + 100*time.Millisecond)
	if value, err, _, reads := read(); err == nil || value != "" || reads != 2 || !strings.Contains(err.Error(), "was not reached") {
		t.Errorf("after the while and with the store down, the value was read as %q after %d reads: %v", value, reads, err)
	}
	held.set(func(s *shelf) { s.down = nil })
	if value, err, logins, reads := read(); err != nil || value != "sk-the-second" || logins != 1 || reads != 3 {
		t.Errorf("after the while, the value was read as %q after %d logins and %d reads: %v", value, logins, reads, err)
	}
	// A value that the store has no longer is gone when the while is over,
	// also from the memory of the Supervisor.
	held.set(func(s *shelf) { delete(s.values, "llm/model api_key") })
	time.Sleep(lasts + 100*time.Millisecond)
	if value, err, _, _ := read(); err == nil || value != "" {
		t.Errorf("a value that was taken out of the store was read as %q: %v", value, err)
	}
}

// A value that is asked for all the time is read again when its time is up
// all the same: asking for a value does not keep it.
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
		t.Errorf("a value that was asked for all the time was read from its store %d times", reads)
	}
}

// A value is taken out of the memory when it is used no longer, also if
// nobody asks for it again.
func TestReadForgetsWhatItUsesNoLonger(t *testing.T) {
	t.Parallel()
	stores, _ := shelved(t, 200*time.Millisecond)
	if _, err := stores.Read(context.Background(), aStore(), "the-credential", "llm/model", "api_key"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); stores.values.Metrics().Evictions == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the Supervisor still holds a value that it uses no longer")
		}
	}
}

// An installation that has nothing remembered reads the store every time.
func TestReadWithNothingRemembered(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, 0)
	from := aStore()
	for expected, value := range []string{"sk-the-first", "sk-the-second", "sk-the-third"} {
		held.set(func(s *shelf) { s.values["llm/model api_key"] = value })
		read, err := stores.Read(context.Background(), from, "the-credential", "llm/model", "api_key")
		if _, reads := held.asked(); err != nil || read != value || reads != expected+1 {
			t.Errorf("with nothing remembered, the value was read as %q after %d reads: %v", read, reads, err)
		}
	}
	if logins, _ := held.asked(); logins != 1 || stores.values.Len() != 0 {
		t.Errorf("with nothing remembered, the store was logged in to %d times and the Supervisor holds %d values", logins, stores.values.Len())
	}
}

// The Supervisor logs in to a store once and reads with that session until
// it ends: when the time that the store gave it is up, or when the store
// takes it no longer.
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
	if _, reads := held.asked(); reads != 4 {
		t.Errorf("the store was read %d times, not once for each value and once more with the session that had ended", reads)
	}
	// The time of the session is up.
	time.Sleep(lasts + 100*time.Millisecond)
	if value, err, logins := read("llm/fourth"); err != nil || value != "sk-fourth" || logins != 3 {
		t.Errorf("after the time of the session, a value was read as %q after %d logins: %v", value, logins, err)
	}

	// A store that takes no login gives nothing, and is asked again the next
	// time.
	other := aStore()
	if value, err := stores.Read(ctx, other, "another-credential", "llm/model", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), "does not take the login") {
		t.Errorf("with a credential that the store does not take, the value was read as %q: %v", value, err)
	}
	if value, err := stores.Read(ctx, other, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
		t.Errorf("after a login that the store refused, the value was read as %q: %v", value, err)
	}
}

// What is remembered of a store is of the store as it was registered last,
// and of that store alone.
func TestReadOfAStoreThatWasReplaced(t *testing.T) {
	t.Parallel()
	stores, held := shelved(t, lasts)
	from := aStore()
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
	other := aStore()
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
			t.Errorf("the store took %s", took)
		}
	}
	from := aStore()
	ctx := context.Background()
	// The store answers when all have asked.
	waiting := make(chan struct{})
	held.set(func(s *shelf) { s.held = waiting })
	var readers sync.WaitGroup
	for range 20 {
		readers.Go(func() {
			if value, err := stores.Read(ctx, from, "the-credential", "llm/model", "api_key"); err != nil || value != "sk-the-first" {
				t.Errorf("the value was read as %q: %v", value, err)
			}
		})
	}
	// A reader that waits no longer for the store leaves, and the others
	// have their value all the same.
	gone, leave := context.WithCancel(ctx)
	leave()
	if _, err := stores.Read(gone, from, "the-credential", "llm/model", "api_key"); !errors.Is(err, context.Canceled) {
		t.Errorf("a reader that waits no longer was answered with: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	close(waiting)
	readers.Wait()
	held.set(func(s *shelf) { s.held = nil })
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
}

// What a Supervisor remembers is no more than it may, however much it is
// asked for: it forgets what it used longest ago.
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
			t.Errorf("the Supervisor remembers %d values, not the %d that it may", kept, maxValues)
		}
		// The first is forgotten and the last is not.
		_, before := held.asked()
		stores.Read(ctx, from, "the-credential", fmt.Sprintf("llm/%d", maxValues+49), "api_key")
		if _, reads := held.asked(); reads != before {
			t.Error("the value that was read last was read again")
		}
		stores.Read(ctx, from, "the-credential", "llm/0", "api_key")
		if _, reads := held.asked(); reads != before+1 {
			t.Error("the value that was read first is still remembered")
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
			t.Errorf("the Supervisor remembers %d values of %d bytes each, which is not about the %d bytes that it may", kept, maxValue, maxBytes)
		}
		// A value that is longer than a secret may be is no secret.
		held.set(func(s *shelf) { s.every = strings.Repeat("k", maxValue+1) })
		if value, err := stores.Read(ctx, from, "the-credential", "llm/long", "api_key"); err == nil || value != "" || !strings.Contains(err.Error(), "longer than a secret may be") {
			t.Errorf("a value that is too long was read as %d bytes: %v", len(value), err)
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
			t.Errorf("the Supervisor holds the sessions of %d stores, not of the %d that it may", kept, maxStores)
		}
		// The store that was asked first is logged in to again.
		before, _ := held.asked()
		if _, err := stores.Read(ctx, first, "the-credential", "llm/model", "api_key"); err != nil {
			t.Fatal(err)
		}
		if logins, _ := held.asked(); logins != before+1 {
			t.Errorf("the store that was asked first was logged in to %d more times", logins-before)
		}
	})
}
