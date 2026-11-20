// Package secret seals the secrets that the Supervisor stores, such as model
// provider API keys, with keys that are kept outside the database. It also
// reads the secrets that projects keep in their own secret stores.
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

// keySize is the size of an AES-256 key in bytes.
const keySize = 32

// ErrSealed is returned when a secret does not open: it was sealed for
// another place or with another key, or it was modified.
var ErrSealed = errors.New("cannot open sealed secret")

// Keys are the encryption keys of an installation. New secrets are sealed
// with the current key, and the other keys still open what they sealed, so a
// key is rotated by adding a new one and making it current.
type Keys struct {
	dir, current string
	mu           sync.RWMutex
	ciphers      map[string]cipher.AEAD
}

// Load reads the keys from dir, in which each file holds a key of 32 random
// bytes in base64, named by the file name. current names the key that new
// secrets are sealed with.
func Load(dir, current string) (*Keys, error) {
	k := &Keys{dir: dir, current: current}
	if err := k.read(); err != nil {
		return nil, err
	}
	if _, found := k.ciphers[current]; !found {
		return nil, fmt.Errorf("%s: current key %q not found", dir, current)
	}
	return k, nil
}

// read reloads the keys from the directory.
func (k *Keys) read() error {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return fmt.Errorf("read encryption keys: %w", err)
	}
	ciphers := map[string]cipher.AEAD{}
	for _, entry := range entries {
		// Skip the entries that Kubernetes adds to a mounted secret, such as
		// ..data.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(k.dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read encryption keys: %w", err)
		}
		key, err := base64.StdEncoding.DecodeString(string(encoded))
		if err != nil || len(key) != keySize {
			return fmt.Errorf("key %q in %s is not %d base64-encoded bytes", entry.Name(), k.dir, keySize)
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

// cipher returns the cipher of the named key. It reloads the keys if the name
// is unknown, so that during a rotation a Supervisor that started before the
// new key was added can open secrets sealed with it.
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
		return nil, fmt.Errorf("%s: key %q not found", k.dir, name)
	}
	return found, nil
}

// Seal encrypts value with the current key. The result opens only for the
// same place, so that, for example, the sealed API key of one provider does
// not open as that of another.
func (k *Keys) Seal(value, place string) ([]byte, error) {
	sealing, err := k.cipher(k.current)
	if err != nil {
		return nil, err
	}
	// The key name, a colon, the nonce and the ciphertext. Key names have no
	// colon, since Kubernetes secret keys cannot contain one.
	sealed := append([]byte(k.current+":"), make([]byte, sealing.NonceSize())...)
	nonce := sealed[len(sealed)-sealing.NonceSize():]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return sealing.Seal(sealed, nonce, []byte(value), []byte(place)), nil
}

// Open returns the secret that was sealed for place. If it does not open, the
// error matches ErrSealed.
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
