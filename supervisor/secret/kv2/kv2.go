// Package kv2 asks the secret stores that keep their secrets as key-value
// pairs in versions and that a program logs in to as a role: those that
// serve the HTTP API known as KV version 2, with the login known as AppRole.
// More than one product serves it.
//
// A store of this kind has these settings:
//
//	{"address": "https://store.example.com:8200", "mount": "secret", "role_id": "..."}
//
// The address is that of the store, the mount is the path at which the store
// has its secrets, and the role ID is that of the role to log in as. The
// credential of the store is the secret ID of the role.
//
// A secret of such a store is at a path below the mount, such as team/model,
// and holds values under keys.
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

// Kind is the name of the kind of store that this package asks.
const Kind = "kv-v2"

// Plugin asks the stores of the kind.
type Plugin struct{}

func (Plugin) Kind() string { return Kind }

// settings are the settings of a store.
type settings struct {
	Address string `json:"address"`
	Mount   string `json:"mount"`
	RoleID  string `json:"role_id"`
}

// read reads the settings of a store and tells what is wrong with them, if
// anything is.
func read(raw json.RawMessage) (settings, error) {
	var store settings
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store); err != nil {
		return store, errors.New("the settings of a store of this kind are its address, its mount and its role_id")
	}
	address, err := url.Parse(store.Address)
	switch {
	case err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Hostname() == "":
		return store, errors.New("address must be a URL that begins with http:// or https://")
	case address.User != nil:
		return store, errors.New("address must hold no user name and no password")
	case strings.Trim(address.Path, "/") != "" || address.RawQuery != "" || address.ForceQuery || address.Fragment != "":
		return store, errors.New("address must end with its host and its port")
	case store.Mount == "" || strings.Trim(store.Mount, "/") != store.Mount:
		return store, errors.New("mount is required: the path at which the store has its secrets, without a slash at either end")
	case store.RoleID == "":
		return store, errors.New("role_id is required")
	}
	store.Address = strings.TrimSuffix(store.Address, "/")
	return store, nil
}

func (Plugin) Address(raw json.RawMessage) (string, error) {
	store, err := read(raw)
	return store.Address, err
}

// Login logs in to the store as the role, with the secret ID of the role. A
// store answers with a token, which is what it is read with, and with how
// long the token is good.
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
		return nil, 0, fmt.Errorf("the store was not reached: %w", err)
	}
	defer response.Body.Close()
	// What the store says beside its status is not passed on: it is of a
	// host that the project named.
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("the store answered the login with the status %d", response.StatusCode)
	}
	var answer struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int64  `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer); err != nil || answer.Auth.ClientToken == "" {
		return nil, 0, errors.New("the store answered the login with no token")
	}
	return answer.Auth.ClientToken, time.Duration(answer.Auth.LeaseDuration) * time.Second, nil
}

// tokenHeader is the header that a request carries the token of a login in.
const tokenHeader = "X-Vault-Token"

// Read reads the secret at the path of the store's mount, with the token of
// a login, and returns the value that it holds under the key.
func (Plugin) Read(ctx context.Context, client *http.Client, raw json.RawMessage, session any, path, key string) (string, error) {
	store, err := read(raw)
	if err != nil {
		return "", err
	}
	token, _ := session.(string)
	steps := strings.Split(path, "/")
	for i, step := range steps {
		if step == "" || step == "." || step == ".." {
			return "", errors.New("path must be the path of a secret in the mount of the store, such as team/model, without a slash at either end")
		}
		steps[i] = url.PathEscape(step)
	}
	if key == "" {
		return "", errors.New("key is required: the name of the value among those that the secret holds")
	}
	response, err := get(ctx, client, store.Address+"/v1/"+store.Mount+"/data/"+strings.Join(steps, "/"), token)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	// What the store says beside its status is not passed on, as with the
	// login.
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", errors.New("the store has no secret at the path")
	case http.StatusForbidden:
		// A store refuses a token that has ended as it refuses a path that
		// the role may not read. It tells which of the two when the token is
		// asked about itself.
		asked, err := get(ctx, client, store.Address+"/v1/auth/token/lookup-self", token)
		if err != nil {
			return "", err
		}
		asked.Body.Close()
		if asked.StatusCode != http.StatusOK {
			return "", secret.ErrSessionEnded
		}
		return "", errors.New("the store does not let the role read the secret at the path")
	default:
		return "", fmt.Errorf("the store answered the read with the status %d", response.StatusCode)
	}
	var answer struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer); err != nil {
		return "", errors.New("the store answered the read with no secret")
	}
	value, found := answer.Data.Data[key]
	if !found {
		return "", errors.New("the secret at the path holds no value under the key")
	}
	text, isText := value.(string)
	if !isText {
		return "", errors.New("the value under the key is not a text")
	}
	return text, nil
}

// get asks a store with the token of a login.
func get(ctx context.Context, client *http.Client, address, token string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(tokenHeader, token)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("the store was not reached: %w", err)
	}
	return response, nil
}
