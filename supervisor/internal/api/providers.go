package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"

	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/outbound"
	"aviary/supervisor/store"
)

// secretValue is a secret in a request. The API never returns secrets.
type secretValue struct {
	Value string `json:"value"`
}

// secretField is a secret in a request: its value, which is stored
// encrypted; its location in one of the project's secret stores, which is
// stored in place of the value; or, for the secret that is stored,
// {"stored": true}, which keeps it.
type secretField struct {
	Value  string `json:"value"`
	Store  string `json:"store"`
	Path   string `json:"path"`
	Key    string `json:"key"`
	Stored bool   `json:"stored"`
}

// stored stands in for a secret in responses.
type stored struct {
	Stored bool `json:"stored"`
}

// secretAnswered returns how a response shows a secret: as stored, by its
// location, or as none.
func secretAnswered(sealed []byte, at *store.SecretReference) any {
	switch {
	case at != nil:
		return at
	case sealed != nil:
		return stored{Stored: true}
	}
	return nil
}

// headerField is a header in a request: its value, or its secret.
type headerField struct {
	Name   string       `json:"name"`
	Value  string       `json:"value"`
	Secret *secretField `json:"secret"`
}

// header is the API representation of a header: its value, or its secret.
type header struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret any    `json:"secret,omitempty"`
}

func headersAnswered(from []store.Header) []header {
	var headers []header
	for _, each := range from {
		headers = append(headers, header{Name: each.Name, Value: each.Value, Secret: secretAnswered(each.Sealed, each.At)})
	}
	return headers
}

// provider is the API representation of a model provider. A stored secret is
// shown only as stored, and a secret in a secret store by its location.
type provider struct {
	store.Provider
	Headers []header `json:"headers,omitempty"`
	APIKey  any      `json:"api_key,omitempty"`
}

func answered(from store.Provider) provider {
	return provider{Provider: from, Headers: headersAnswered(from.Headers), APIKey: secretAnswered(from.APIKey, from.APIKeyAt)}
}

// readProvider decodes and validates a request to create the provider with
// the ID, or to replace the existing one.
func (a *api) readProvider(w http.ResponseWriter, r *http.Request, id, projectID uuid.UUID, existing *store.Provider) (store.Provider, error) {
	var request struct {
		Name    string        `json:"name"`
		URL     string        `json:"url"`
		Headers []headerField `json:"headers"`
		APIKey  *secretField  `json:"api_key"`
	}
	if err := decode(w, r, &request); err != nil {
		return store.Provider{}, err
	}
	if request.Name == "" {
		return store.Provider{}, invalid("name is required")
	}
	read, err := a.providerAt(r, id, projectID, existing, request.URL, request.Headers, request.APIKey)
	read.Name = request.Name
	return read, err
}

// providerAt validates the URL, the headers and the key of the provider with
// the ID, and returns the provider without a name. A secret given by value is
// sealed for its place in that provider. A secret in a secret store is read
// once, so that one that cannot be read is reported at once rather than
// during a turn. An existing provider keeps the secrets that it stores only
// while its URL stays the same, so that they go to no other endpoint.
func (a *api) providerAt(r *http.Request, id, projectID uuid.UUID, existing *store.Provider, address string, headers []headerField, key *secretField) (store.Provider, error) {
	if err := endpoint(address, "set the endpoint's key in api_key"); err != nil {
		return store.Provider{}, err
	}
	var had store.Provider
	if existing != nil {
		had = *existing
	}
	keeps := existing != nil && existing.URL == address
	kept, err := a.headersAt(r, projectID, headers, false, had.Headers, keeps, func(name string) string { return store.ProviderHeaderPlace(id, name) })
	if err != nil {
		return store.Provider{}, err
	}
	read := store.Provider{ID: id, URL: address, Headers: kept}
	if key != nil {
		if read.APIKey, read.APIKeyAt, err = a.secretOf(r, projectID, "api_key", store.ProviderKeyPlace(id), *key, had.APIKey, keeps); err != nil {
			return store.Provider{}, err
		}
	}
	return read, nil
}

// headersAt validates the headers of an endpoint, and returns them as they are
// kept. authorization is as for headersOf. had are the headers that the
// endpoint has, whose stored secrets the request can keep if keeps is true,
// and place returns the place that the secret of a header is sealed for.
func (a *api) headersAt(r *http.Request, projectID uuid.UUID, given []headerField, authorization bool, had []store.Header, keeps bool,
	place func(name string) string) ([]store.Header, error) {
	headers, err := headersOf(given, authorization)
	if err != nil {
		return nil, err
	}
	var kept []store.Header
	for i, header := range headers {
		keep := store.Header{Name: header.Name, Value: header.Value}
		if header.Secret != nil {
			var before []byte
			if at := slices.IndexFunc(had, func(other store.Header) bool { return other.Name == header.Name }); at >= 0 {
				before = had[at].Sealed
			}
			where := fmt.Sprintf("headers[%d].secret", i)
			if keep.Sealed, keep.At, err = a.secretOf(r, projectID, where, place(header.Name), *header.Secret, before, keeps); err != nil {
				return nil, err
			}
		}
		kept = append(kept, keep)
	}
	return kept, nil
}

// secretOf returns a secret of a request as it is kept: sealed for the place,
// or located in a secret store of the project. had is the secret that is
// stored in its place, if there is one, which the request can keep if keeps
// is true. where names the secret in what is refused.
func (a *api) secretOf(r *http.Request, projectID uuid.UUID, where, place string, given secretField, had []byte, keeps bool) ([]byte, *store.SecretReference, error) {
	located := given.Store != "" || given.Path != "" || given.Key != ""
	switch {
	case given.Stored && given.Value == "" && !located:
		switch {
		case had == nil:
			return nil, nil, invalid(where + " has no stored secret to keep")
		case !keeps:
			return nil, nil, invalid(where + " must be given again: the URL changed")
		}
		return had, nil, nil
	case given.Value != "" && !located && !given.Stored:
		sealed, err := a.keys.Seal(given.Value, place)
		return sealed, nil, err
	case given.Value == "" && !given.Stored && given.Store != "" && given.Path != "" && given.Key != "":
		at, err := a.inStore(r, projectID, where, given)
		if err != nil {
			return nil, nil, err
		}
		return nil, &at, nil
	}
	return nil, nil, invalid(where + ` must be {"value": "…"} for a secret that Aviary stores, {"store": "…", "path": "…", "key": "…"} ` +
		`for one in a secret store of the project, or {"stored": true} for the one that is stored`)
}

// inStore checks that the secret is in a secret store of the project and can
// be read.
func (a *api) inStore(r *http.Request, projectID uuid.UUID, where string, secret secretField) (store.SecretReference, error) {
	id, err := uuid.Parse(secret.Store)
	if err != nil {
		return store.SecretReference{}, invalid(where + ".store must be the ID of a secret store of the project")
	}
	in, err := a.store.SecretStore(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && in.ProjectID != projectID):
		return store.SecretReference{}, invalid(where + ".store must be the ID of a secret store of the project")
	case err != nil:
		return store.SecretReference{}, err
	}
	credential, err := a.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
	if err != nil {
		return store.SecretReference{}, err
	}
	if _, err := a.stores.Read(r.Context(), in, credential, secret.Path, secret.Key); err != nil {
		return store.SecretReference{}, invalid("cannot read " + where + " from the secret store: " + err.Error())
	}
	return store.SecretReference{Store: id, Path: secret.Path, Key: secret.Key}, nil
}

// The headers of an endpoint are few and small: every request to it carries
// them. A secret whose length is not known counts as secretBytes.
const (
	maxHeaders     = 20
	maxHeaderBytes = 8192
	secretBytes    = 64
)

// reservedHeaders are the headers that an endpoint cannot have, besides
// those of Aviary, of the egress gateway and of MCP: they say where a request
// goes or how it is carried.
var reservedHeaders = []string{
	"connection", "content-length", "expect", "host", "keep-alive", "proxy-authenticate", "proxy-authorization",
	"proxy-connection", "te", "trailer", "transfer-encoding", "upgrade",
}

// headersOf validates the headers of an endpoint, and returns them with their
// names in lowercase and their values without spaces at their ends. A header
// has a value or a secret. authorization is one of them only for an endpoint
// that takes no key of its own.
func headersOf(given []headerField, authorization bool) ([]headerField, error) {
	if len(given) > maxHeaders {
		return nil, invalid(fmt.Sprintf("headers must be at most %d", maxHeaders))
	}
	var headers []headerField
	size := 0
	for i, header := range given {
		at := fmt.Sprintf("headers[%d]", i)
		name, value := strings.ToLower(header.Name), strings.Trim(header.Value, " \t")
		switch {
		case !httpguts.ValidHeaderFieldName(name):
			return nil, invalid(at + ".name must be the name of an HTTP header")
		case name == "authorization" && !authorization:
			return nil, invalid(at + ".name must not be authorization; set the endpoint's key in api_key")
		case slices.Contains(reservedHeaders, name) || strings.HasPrefix(name, "x-aviary-") || strings.HasPrefix(name, "x-envoy-") ||
			strings.HasPrefix(name, "mcp-"):
			return nil, invalid(at + ".name must not be " + name + ", which is reserved")
		case slices.ContainsFunc(headers, func(other headerField) bool { return other.Name == name }):
			return nil, invalid(at + ".name must not be " + name + ", which another header has")
		case header.Secret != nil && header.Value != "":
			return nil, invalid(at + " must have a value or a secret")
		case header.Secret != nil:
			secret := *header.Secret
			secret.Value = strings.Trim(secret.Value, " \t")
			if !httpguts.ValidHeaderFieldValue(secret.Value) {
				return nil, invalid(at + ".secret.value must be the value of an HTTP header")
			}
			size += len(name) + max(len(secret.Value), secretBytes)
			headers = append(headers, headerField{Name: name, Secret: &secret})
			continue
		case value == "" || !httpguts.ValidHeaderFieldValue(value):
			return nil, invalid(at + ".value must be the value of an HTTP header, and not empty")
		}
		size += len(name) + len(value)
		headers = append(headers, headerField{Name: name, Value: value})
	}
	if size > maxHeaderBytes {
		return nil, invalid(fmt.Sprintf("the names and values of headers must be at most %d bytes together", maxHeaderBytes))
	}
	return headers, nil
}

// endpoint validates the base URL of an endpoint. secrets says where the
// secrets of the endpoint go instead of the URL.
func endpoint(address, secrets string) error {
	parsed, err := url.Parse(address)
	switch {
	case err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "":
		return invalid("url must be a URL that begins with http:// or https://")
	case parsed.User != nil:
		return invalid("url must not contain a user name or password; " + secrets)
	case parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "":
		return invalid("url must not have a query or fragment")
	}
	return nil
}

func (a *api) createProvider(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	read, err := a.readProvider(w, r, id, projectID, nil)
	if err != nil {
		return err
	}
	read.ProjectID = projectID
	created, err := a.store.CreateProvider(r.Context(), read)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, answered(created))
	return nil
}

// listTimeout is how long the model request of a listing may take. It is a
// variable for tests.
var listTimeout = 15 * time.Second

// maxListBytes limits how much of an endpoint's list of models is read.
const maxListBytes = 1 << 20

// listed is what came of asking a model endpoint for its models.
type listed struct {
	// Reached reports whether the endpoint answered. Status is its answer,
	// and Reason is why there was none.
	Reached bool   `json:"reached"`
	Status  int    `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Models are the IDs of the models that the endpoint listed, if it
	// answered with a list of models.
	Models     []string `json:"models,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

// endpointModels lists the models of a model endpoint that need not be
// registered: the request gives the URL, the headers and the key of a
// provider, which is not stored. A provider that the request names lends the
// secrets that it stores, as it would keep them if it were replaced.
func (a *api) endpointModels(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	var request struct {
		URL      string        `json:"url"`
		Headers  []headerField `json:"headers"`
		APIKey   *secretField  `json:"api_key"`
		Provider string        `json:"provider"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var existing *store.Provider
	if request.Provider != "" {
		named, err := uuid.Parse(request.Provider)
		if err != nil {
			return invalid("provider must be the ID of a model provider of the project")
		}
		found, err := a.store.Provider(r.Context(), named)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && found.ProjectID != projectID):
			return invalid("provider must be the ID of a model provider of the project")
		case err != nil:
			return err
		}
		existing, id = &found, found.ID
	}
	provider, err := a.providerAt(r, id, projectID, existing, request.URL, request.Headers, request.APIKey)
	if err != nil {
		return err
	}
	provider.ProjectID = projectID
	return a.listModels(w, r, provider)
}

// providerModels lists the models of a provider.
func (a *api) providerModels(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "provider")
	if err != nil {
		return err
	}
	provider, err := a.store.Provider(r.Context(), id)
	if err != nil {
		return err
	}
	return a.listModels(w, r, provider)
}

// listModels asks the endpoint of the provider for its models, with the
// provider's headers and key, as the model requests of a turn have them.
func (a *api) listModels(w http.ResponseWriter, r *http.Request, provider store.Provider) error {
	result, err := a.ask(r.Context(), provider)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, result)
	return nil
}

// ask sends the provider's endpoint the model request of a listing, GET
// models under its URL, and returns what came of it.
func (a *api) ask(ctx context.Context, provider store.Provider) (listed, error) {
	at, err := url.Parse(provider.URL)
	if err != nil {
		return listed{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(at.String(), "/")+"/models", nil)
	if err != nil {
		return listed{}, err
	}
	var in *store.SecretStore
	if keyAt := provider.APIKeyAt; keyAt != nil {
		found, err := a.store.SecretStore(ctx, keyAt.Store)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && found.ProjectID != provider.ProjectID):
		case err != nil:
			return listed{}, err
		default:
			in = &found
		}
	}
	key, err := a.resolver.ProviderKey(ctx, provider, in)
	if errors.Is(err, credentials.ErrUnreadable) {
		return listed{Reason: err.Error()}, nil
	}
	if err != nil {
		return listed{}, err
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for _, header := range provider.Headers {
		value, err := a.resolver.Header(ctx, provider.ProjectID, header, store.ProviderHeaderPlace(provider.ID, header.Name), "the model provider's header "+header.Name)
		if errors.Is(err, credentials.ErrUnreadable) {
			return listed{Reason: err.Error()}, nil
		}
		if err != nil {
			return listed{}, err
		}
		request.Header.Set(header.Name, value)
	}
	began := time.Now()
	answer, err := a.outbound.Do(request)
	took := time.Since(began).Milliseconds()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return listed{Reason: "the endpoint did not answer in " + listTimeout.String(), DurationMS: took}, nil
	case errors.Is(err, context.Canceled):
		return listed{}, err
	case outbound.Refused(err):
		return listed{Reason: "the endpoint's address is not reached", DurationMS: took}, nil
	case err != nil:
		slog.InfoContext(ctx, "ask a model provider for its models", "provider", provider.ID, "error", err)
		return listed{Reason: "the endpoint cannot be reached", DurationMS: took}, nil
	}
	defer answer.Body.Close()
	result := listed{Reached: true, Status: answer.StatusCode, DurationMS: took}
	if answer.StatusCode == http.StatusOK {
		result.Models = modelsIn(answer.Body)
	}
	return result, nil
}

// modelsIn returns the IDs of the models in a list of models as the OpenAI
// API gives it, {"data": [{"id": "..."}]}, or nil if the body is not one.
func modelsIn(body io.Reader) []string {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(body, maxListBytes)).Decode(&list); err != nil {
		return nil
	}
	var models []string
	for _, model := range list.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models
}

func (a *api) getProvider(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "provider")
	if err != nil {
		return err
	}
	found, err := a.store.Provider(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, answered(found))
	return nil
}

func (a *api) listProviders(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	found, err := a.store.Providers(r.Context(), projectID)
	if err != nil {
		return err
	}
	providers := make([]provider, len(found))
	for i, from := range found {
		providers[i] = answered(from)
	}
	respond(w, http.StatusOK, struct {
		Providers []provider `json:"providers"`
	}{providers})
	return nil
}

// replaceProvider replaces the name, URL, headers and key of a provider
// together. The secrets that it stores stay only with its URL, so that they
// cannot be redirected to another endpoint.
func (a *api) replaceProvider(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "provider")
	if err != nil {
		return err
	}
	existing, err := a.store.Provider(r.Context(), id)
	if err != nil {
		return err
	}
	read, err := a.readProvider(w, r, id, existing.ProjectID, &existing)
	if err != nil {
		return err
	}
	replaced, err := a.store.ReplaceProvider(r.Context(), read)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, answered(replaced))
	return nil
}
