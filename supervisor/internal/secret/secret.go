// Package secret seals the secrets that the Supervisor stores, such as the
// API key of a model provider, and opens them when they are used. What is
// stored is of no use without the keys, which the database does not hold.
package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// keySize is the size of a key, which is one of AES-256.
const keySize = 32

// ErrSealed is returned for what does not open: it was sealed for another
// place or with another key, or it was changed since.
var ErrSealed = errors.New("the secret does not open")

// Keys are the keys of an installation. Secrets are sealed with one of them.
// The others still open what was sealed with them, so the key of an
// installation is replaced by adding one and sealing with it from then on.
type Keys struct {
	dir, current string
	mu           sync.RWMutex
	ciphers      map[string]cipher.AEAD
}

// Load reads the keys from a directory, in which each file is a key under
// its name: 32 random bytes in base64. Secrets are sealed with the key that
// is named.
func Load(dir, current string) (*Keys, error) {
	k := &Keys{dir: dir, current: current}
	if err := k.read(); err != nil {
		return nil, err
	}
	if _, found := k.ciphers[current]; !found {
		return nil, fmt.Errorf("%s has no key named %q to seal secrets with", dir, current)
	}
	return k, nil
}

// read reads the keys that the directory holds now.
func (k *Keys) read() error {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return fmt.Errorf("read the keys that secrets are sealed with: %w", err)
	}
	ciphers := map[string]cipher.AEAD{}
	for _, entry := range entries {
		// Kubernetes keeps what it mounts of a secret in entries of its own,
		// whose names begin with dots.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(k.dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read the keys that secrets are sealed with: %w", err)
		}
		key, err := base64.StdEncoding.DecodeString(string(encoded))
		if err != nil || len(key) != keySize {
			return fmt.Errorf("the key %q in %s is not %d bytes in base64", entry.Name(), k.dir, keySize)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		if ciphers[entry.Name()], err = cipher.NewGCM(block); err != nil {
			return err
		}
	}
	k.mu.Lock()
	k.ciphers = ciphers
	k.mu.Unlock()
	return nil
}

// cipher returns the cipher of the key with the name. A key that was added
// since the keys were read is found by reading them again: a Supervisor of
// the release before opens what one of the next sealed with a new key.
func (k *Keys) cipher(name string) (cipher.AEAD, error) {
	k.mu.RLock()
	found, known := k.ciphers[name]
	k.mu.RUnlock()
	if known {
		return found, nil
	}
	if err := k.read(); err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if found, known = k.ciphers[name]; !known {
		return nil, fmt.Errorf("%s has no key named %q", k.dir, name)
	}
	return found, nil
}

// Seal returns the secret as it is stored. It opens only for the same place:
// what was sealed as the key of one provider is not the key of another.
func (k *Keys) Seal(value, place string) ([]byte, error) {
	sealing, err := k.cipher(k.current)
	if err != nil {
		return nil, err
	}
	// The name of the key, a colon, the nonce and what the cipher makes of
	// the value. The name of an entry of a Kubernetes secret has no colon.
	sealed := append([]byte(k.current+":"), make([]byte, sealing.NonceSize())...)
	nonce := sealed[len(sealed)-sealing.NonceSize():]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return sealing.Seal(sealed, nonce, []byte(value), []byte(place)), nil
}

// Open returns the secret that was sealed for the place.
func (k *Keys) Open(sealed []byte, place string) (string, error) {
	name, rest, found := bytes.Cut(sealed, []byte(":"))
	if !found {
		return "", ErrSealed
	}
	opening, err := k.cipher(string(name))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrSealed, err)
	}
	if len(rest) < opening.NonceSize() {
		return "", ErrSealed
	}
	value, err := opening.Open(nil, rest[:opening.NonceSize()], rest[opening.NonceSize():], []byte(place))
	if err != nil {
		return "", ErrSealed
	}
	return string(value), nil
}
