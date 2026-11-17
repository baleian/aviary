package store_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"aviary/supervisor/store"
)

// kind is a plugin of the tests, which tells what it was asked.
type kind struct {
	schemes []string
	asked   *[]string
}

func (k kind) Schemes() []string { return k.schemes }

func (k kind) Open(_ context.Context, address string) (store.Store, error) {
	*k.asked = append(*k.asked, k.schemes[0]+" open "+address)
	return nil, errors.New("the database of the tests is none")
}

func (k kind) Migrate(_ context.Context, address string) error {
	*k.asked = append(*k.asked, k.schemes[0]+" migrate "+address)
	return nil
}

func (k kind) MigrateTo(_ context.Context, address string, version int64) error {
	*k.asked = append(*k.asked, k.schemes[0]+" migrate "+address+" to "+string(rune('0'+version)))
	return nil
}

// The address of the database says which plugin connects to it.
func TestPluginIsChosenByTheScheme(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var asked []string
	plugins := []store.Plugin{kind{[]string{"first", "firstly"}, &asked}, kind{[]string{"second"}, &asked}}
	if _, err := store.Open(ctx, "firstly://host/records", plugins...); err == nil || !strings.Contains(err.Error(), "none") {
		t.Errorf("the database was opened with: %v", err)
	}
	if err := store.Migrate(ctx, "second://user:password@host/records", plugins...); err != nil {
		t.Error(err)
	}
	if err := store.MigrateTo(ctx, "first://host/records", 7, plugins...); err != nil {
		t.Error(err)
	}
	expected := []string{"first open firstly://host/records", "second migrate second://user:password@host/records", "first migrate first://host/records to 7"}
	if !slices.Equal(asked, expected) {
		t.Errorf("the plugins were asked %q, not %q", asked, expected)
	}

	// A Supervisor that has no plugin for its database does not start, and
	// says why without the password.
	for address, why := range map[string]string{
		"third://user:secret@host/records": "first, firstly, second",
		"host=records user=secret":         "first, firstly, second",
		"first://user:secret@host:port/a":  "parse",
	} {
		for name, err := range map[string]error{
			"open":       func() error { _, err := store.Open(ctx, address, plugins...); return err }(),
			"migrate":    store.Migrate(ctx, address, plugins...),
			"migrate to": store.MigrateTo(ctx, address, 7, plugins...),
		} {
			if err == nil || !strings.Contains(err.Error(), why) || strings.Contains(err.Error(), "secret") {
				t.Errorf("%s of the database at %s: %v", name, address, err)
			}
		}
	}
	if len(asked) != len(expected) {
		t.Errorf("a plugin was asked about a database that is not of its kind: %q", asked)
	}
}

func TestTokens(t *testing.T) {
	t.Parallel()
	first, second := store.NewToken(), store.NewToken()
	if first == second || len(first) < 20 {
		t.Errorf("the tokens of two turns are %q and %q", first, second)
	}
	hash := store.HashToken(first)
	if string(hash) == first || !slices.Equal(hash, store.HashToken(first)) || slices.Equal(hash, store.HashToken(second)) {
		t.Errorf("what is kept of the token %q is %x", first, hash)
	}
}
