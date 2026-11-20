// Package kv2 reads secrets from stores that serve the KV version 2 HTTP API
// with AppRole login. More than one product serves this API.
//
// A store of this kind has these settings:
//
//	{"address": "https://store.example.com:8200", "mount": "secret", "role_id": "..."}
//
// The address is the URL of the store, the mount is the path at which the
// store keeps the secrets, and role_id is the role to log in as. The
// credential of the store is the secret ID of the role.
//
// A secret is at a path below the mount, such as team/model, and holds
// values under keys.
package kv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aviary/supervisor/secret"
)

// Kind is the secret store kind that this package serves.
const Kind = "kv-v2"

// Plugin is the secret.Plugin for stores of this kind.
type Plugin struct{}

func (Plugin) Kind() string { return Kind }

type settings struct {
	Address string `json:"address"`
	Mount   string `json:"mount"`
	RoleID  string `json:"role_id"`
}

// read parses and validates the settings of a store.
func read(raw json.RawMessage) (settings, error) {
	var store settings
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store); err != nil {
		return store, errors.New("expected an object with address, mount and role_id")
	}
	address, err := url.Parse(store.Address)
	switch {
	case err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Hostname() == "":
		return store, errors.New("address must be an http:// or https:// URL")
	case address.User != nil:
		return store, errors.New("address must not contain a user name or password")
	case strings.Trim(address.Path, "/") != "" || address.RawQuery != "" || address.ForceQuery || address.Fragment != "":
		return store, errors.New("address must not have a path, query or fragment")
	case store.RoleID == "":
		return store, errors.New("role_id is required")
	}
	mount, ok := escaped(store.Mount)
	if !ok {
		return store, errors.New("mount is required: a path such as secret, without empty or dot segments")
	}
	store.Mount = mount
	store.Address = strings.TrimSuffix(store.Address, "/")
	return store, nil
}

// escaped returns path with its segments escaped for a URL. It reports false
// if a segment is empty or a dot segment.
func escaped(path string) (string, bool) {
	steps := strings.Split(path, "/")
	for i, step := range steps {
		if step == "" || step == "." || step == ".." {
			return "", false
		}
		steps[i] = url.PathEscape(step)
	}
	return strings.Join(steps, "/"), true
}

func (Plugin) Address(raw json.RawMessage) (string, error) {
	store, err := read(raw)
	return store.Address, err
}

// Login logs in as the role with its secret ID, and returns the client token
// and its lease duration.
func (Plugin) Login(ctx context.Context, client *http.Client, raw json.RawMessage, secretID string) (any, time.Duration, error) {
	store, err := read(raw)
	if err != nil {
		return nil, 0, err
	}
	body, err := json.Marshal(map[string]string{"role_id": store.RoleID, "secret_id": secretID})
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, store.Address+"/v1/auth/approle/login", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("connect to the store: %w", err)
	}
	defer response.Body.Close()
	// Errors report only the status, since the body comes from a host that
	// the project chose.
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("store returned status %d", response.StatusCode)
	}
	var answer struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int64  `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer); err != nil || answer.Auth.ClientToken == "" {
		return nil, 0, errors.New("store returned no client token")
	}
	return answer.Auth.ClientToken, time.Duration(answer.Auth.LeaseDuration) * time.Second, nil
}

// tokenHeader carries the client token.
const tokenHeader = "X-Vault-Token"

// Read reads the secret at path below the mount, and returns its value under
// key.
func (Plugin) Read(ctx context.Context, client *http.Client, raw json.RawMessage, session any, path, key string) (string, error) {
	store, err := read(raw)
	if err != nil {
		return "", err
	}
	token, _ := session.(string)
	steps, ok := escaped(path)
	if !ok {
		return "", errors.New("path must be a secret path below the mount, such as team/model, without empty or dot segments")
	}
	if key == "" {
		return "", errors.New("key is required")
	}
	response, err := get(ctx, client, store.Address+"/v1/"+store.Mount+"/data/"+steps, token)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	// As in Login, errors report only the status.
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", errors.New("secret not found")
	case http.StatusForbidden:
		// The store answers 403 both for a token that is no longer valid and
		// for a path that the role may not read. A token lookup tells them
		// apart.
		asked, err := get(ctx, client, store.Address+"/v1/auth/token/lookup-self", token)
		if err != nil {
			return "", err
		}
		asked.Body.Close()
		if asked.StatusCode != http.StatusOK {
			return "", secret.ErrSessionEnded
		}
		return "", errors.New("role is not allowed to read the secret")
	default:
		return "", fmt.Errorf("store returned status %d", response.StatusCode)
	}
	var answer struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer); err != nil {
		return "", errors.New("store returned no secret")
	}
	value, found := answer.Data.Data[key]
	if !found {
		return "", errors.New("secret has no such key")
	}
	text, isText := value.(string)
	if !isText {
		return "", errors.New("value is not a string")
	}
	return text, nil
}

// get sends a GET request with the client token.
func get(ctx context.Context, client *http.Client, address, token string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(tokenHeader, token)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("connect to the store: %w", err)
	}
	return response, nil
}
