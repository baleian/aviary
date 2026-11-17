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
