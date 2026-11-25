package store_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"aviary/supervisor/store"
)

// kind is a fake plugin that records the calls it gets.
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

func TestPluginIsChosenByTheScheme(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var asked []string
	plugins := []store.Plugin{kind{[]string{"first", "firstly"}, &asked}, kind{[]string{"second"}, &asked}}
	if _, err := store.Open(ctx, "firstly://host/records", plugins...); err == nil || !strings.Contains(err.Error(), "none") {
		t.Errorf("Open = %v, want the plugin's error", err)
	}
	if err := store.Migrate(ctx, "second://user:password@host/records", plugins...); err != nil {
		t.Error(err)
	}
	if err := store.MigrateTo(ctx, "first://host/records", 7, plugins...); err != nil {
		t.Error(err)
	}
	expected := []string{"first open firstly://host/records", "second migrate second://user:password@host/records", "first migrate first://host/records to 7"}
	if !slices.Equal(asked, expected) {
		t.Errorf("plugin calls = %q, want %q", asked, expected)
	}

	// An address that no plugin handles, or that cannot be parsed, fails with
	// an error that does not reveal the password.
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
				t.Errorf("%s %s: error = %v, want the reason without the password", name, address, err)
			}
		}
	}
	if len(asked) != len(expected) {
		t.Errorf("unexpected plugin calls: %q", asked)
	}
}

// Names that occur again are changed before their extension, so that all
// differ.
func TestUniqueNames(t *testing.T) {
	got := store.UniqueNames([]string{"a.png", "b", "a.png", "a (2).png", "b", "a.png", ".env", ".env", "a.tar.gz", "a.tar.gz"})
	want := []string{"a.png", "b", "a (2).png", "a (2) (2).png", "b (2)", "a (3).png", ".env", ".env (2)", "a.tar.gz", "a.tar (2).gz"}
	if !slices.Equal(got, want) {
		t.Errorf("UniqueNames = %q, want %q", got, want)
	}
	if none := store.UniqueNames(nil); len(none) != 0 {
		t.Errorf("UniqueNames of no names = %q", none)
	}
}
