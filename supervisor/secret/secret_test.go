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

// add writes a new random key named name to dir.
func add(t *testing.T, dir, name string) {
	t.Helper()
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	// A key written by hand ends with a newline.
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
		t.Errorf("sealed value contains the secret: %q", sealed)
	}
	if opened, err := keys.Open(sealed, place); err != nil || opened != value {
		t.Errorf("Open = %q, %v", opened, err)
	}
	if again := seal(t, keys, value, place); bytes.Equal(again, sealed) {
		t.Error("sealing the same secret twice gave the same bytes")
	}

	if opened, err := keys.Open(sealed, "providers/2/api_key"); !errors.Is(err, ErrSealed) {
		t.Errorf("Open in another place = %q, %v; want ErrSealed", opened, err)
	}
	changed := bytes.Clone(sealed)
	changed[len(changed)-1] ^= 1
	if opened, err := keys.Open(changed, place); !errors.Is(err, ErrSealed) {
		t.Errorf("Open of a modified secret = %q, %v; want ErrSealed", opened, err)
	}
	for _, short := range [][]byte{nil, []byte("k1"), []byte("k1:"), sealed[:len("k1:")+4]} {
		if opened, err := keys.Open(short, place); !errors.Is(err, ErrSealed) {
			t.Errorf("Open(%q) = %q, %v; want ErrSealed", short, opened, err)
		}
	}
}

func TestKeyIsReplaced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	add(t, dir, "k1")
	before := load(t, dir, "k1")
	old := seal(t, before, "old", "place")

	// Rotate by adding a key and making it current, while a Supervisor that
	// loaded the keys before keeps running.
	add(t, dir, "k2")
	after := load(t, dir, "k2")
	if opened, err := after.Open(old, "place"); err != nil || opened != "old" {
		t.Errorf("Open of a secret sealed with the old key = %q, %v", opened, err)
	}
	sealed := seal(t, after, "new", "place")
	if !bytes.HasPrefix(sealed, []byte("k2:")) {
		t.Errorf("secret not sealed with the new key: %q", sealed)
	}
	if opened, err := before.Open(sealed, "place"); err != nil || opened != "new" {
		t.Errorf("Open with keys loaded before the rotation = %q, %v", opened, err)
	}

	// A secret does not open without its key.
	if err := os.Remove(filepath.Join(dir, "k1")); err != nil {
		t.Fatal(err)
	}
	if opened, err := load(t, dir, "k2").Open(old, "place"); !errors.Is(err, ErrSealed) || !strings.Contains(err.Error(), `"k1"`) {
		t.Errorf("Open without its key = %q, %v; want ErrSealed naming the key", opened, err)
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
		t.Errorf("Load without the current key: %v, want an error naming it", err)
	}
	if _, err := Load(filepath.Join(missing, "none"), "k1"); err == nil {
		t.Error("Load from a missing directory succeeded")
	}
	for name, content := range map[string]string{"short": short, "plain": "not a key in base64"} {
		dir := t.TempDir()
		add(t, dir, "k1")
		write(dir, name, content)
		if _, err := Load(dir, "k1"); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("Load with key file %s: %v, want an error naming it", name, err)
		}
	}

	// The entries that Kubernetes adds to a mounted secret are skipped.
	mounted := t.TempDir()
	add(t, mounted, "k1")
	if err := os.Mkdir(filepath.Join(mounted, "..data"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(mounted, "..2026_11_17", "")
	if _, err := Load(mounted, "k1"); err != nil {
		t.Errorf("Load from a mounted secret: %v", err)
	}
}
