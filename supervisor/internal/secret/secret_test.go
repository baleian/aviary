package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// add writes a new key under the name to the directory.
func add(t *testing.T, dir, name string) {
	t.Helper()
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	// A key that was written by hand ends with a line break.
	if err := os.WriteFile(filepath.Join(dir, name), []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, dir, current string) *Keys {
	t.Helper()
	keys, err := Load(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func seal(t *testing.T, keys *Keys, value, place string) []byte {
	t.Helper()
	sealed, err := keys.Seal(value, place)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestSealedSecretOpensOnlyInItsPlace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	add(t, dir, "k1")
	keys := load(t, dir, "k1")
	const value, place = "sk-the-api-key", "providers/1/api_key"

	sealed := seal(t, keys, value, place)
	if bytes.Contains(sealed, []byte(value)) {
		t.Errorf("what is stored holds the secret: %q", sealed)
	}
	if opened, err := keys.Open(sealed, place); err != nil || opened != value {
		t.Errorf("the secret opened as %q, %v", opened, err)
	}
	if again := seal(t, keys, value, place); bytes.Equal(again, sealed) {
		t.Error("the same secret was sealed to the same bytes twice")
	}

	if opened, err := keys.Open(sealed, "providers/2/api_key"); !errors.Is(err, ErrSealed) {
		t.Errorf("in another place the secret opened as %q, %v", opened, err)
	}
	changed := bytes.Clone(sealed)
	changed[len(changed)-1] ^= 1
	if opened, err := keys.Open(changed, place); !errors.Is(err, ErrSealed) {
		t.Errorf("a changed secret opened as %q, %v", opened, err)
	}
	for _, short := range [][]byte{nil, []byte("k1"), []byte("k1:"), sealed[:len("k1:")+4]} {
		if opened, err := keys.Open(short, place); !errors.Is(err, ErrSealed) {
			t.Errorf("%q opened as %q, %v", short, opened, err)
		}
	}
}

func TestKeyIsReplaced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	add(t, dir, "k1")
	before := load(t, dir, "k1")
	old := seal(t, before, "old", "place")

	// The key is replaced by adding one and naming it. A Supervisor that
	// started before still runs.
	add(t, dir, "k2")
	after := load(t, dir, "k2")
	if opened, err := after.Open(old, "place"); err != nil || opened != "old" {
		t.Errorf("what was sealed with the key before opened as %q, %v", opened, err)
	}
	sealed := seal(t, after, "new", "place")
	if !bytes.HasPrefix(sealed, []byte("k2:")) {
		t.Errorf("the secret was not sealed with the new key: %q", sealed)
	}
	if opened, err := before.Open(sealed, "place"); err != nil || opened != "new" {
		t.Errorf("the Supervisor that started before opened it as %q, %v", opened, err)
	}

	// Without the key that it was sealed with, a secret does not open.
	if err := os.Remove(filepath.Join(dir, "k1")); err != nil {
		t.Fatal(err)
	}
	if opened, err := load(t, dir, "k2").Open(old, "place"); !errors.Is(err, ErrSealed) || !strings.Contains(err.Error(), `"k1"`) {
		t.Errorf("without its key the secret opened as %q, %v", opened, err)
	}
}

func TestKeysThatCannotBeUsed(t *testing.T) {
	t.Parallel()
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))

	missing := t.TempDir()
	add(t, missing, "k1")
	if _, err := Load(missing, "k2"); err == nil || !strings.Contains(err.Error(), `"k2"`) {
		t.Errorf("without the key to seal with, the keys were loaded: %v", err)
	}
	if _, err := Load(filepath.Join(missing, "none"), "k1"); err == nil {
		t.Error("the keys were loaded from a directory that does not exist")
	}
	for name, content := range map[string]string{"short": short, "plain": "not a key in base64"} {
		dir := t.TempDir()
		add(t, dir, "k1")
		write(dir, name, content)
		if _, err := Load(dir, "k1"); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("a key that is %s was loaded: %v", name, err)
		}
	}

	// What Kubernetes keeps beside the entries of a secret is no key.
	mounted := t.TempDir()
	add(t, mounted, "k1")
	if err := os.Mkdir(filepath.Join(mounted, "..data"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(mounted, "..2026_11_17", "")
	if _, err := Load(mounted, "k1"); err != nil {
		t.Errorf("the keys of a mounted secret were not loaded: %v", err)
	}
}
