package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/authz"
	"aviary/supervisor/store"
)

// secretValue is a secret in a request. The API never returns secrets.
type secretValue struct {
	Value string `json:"value"`
}

// secretField is a secret in a request: either its value, which is stored
// encrypted, or its location in one of the project's secret stores, which
// is stored in place of the value.
type secretField struct {
	Value string `json:"value"`
	Store string `json:"store"`
	Path  string `json:"path"`
	Key   string `json:"key"`
}

// stored stands in for a secret in responses.
type stored struct {
	Stored bool `json:"stored"`
}

// provider is the API representation of a model provider. A stored key is
// shown only as stored, and a key in a secret store by its location.
type provider struct {
	store.Provider
	APIKey any `json:"api_key,omitempty"`
}

func answered(from store.Provider) provider {
	answer := provider{Provider: from}
	switch {
	case from.APIKeyAt != nil:
		answer.APIKey = from.APIKeyAt
	case from.APIKey != nil:
		answer.APIKey = stored{Stored: true}
	}
	return answer
}

// readProvider decodes and validates a request to create or replace the
// provider with the ID.
func (a *api) readProvider(w http.ResponseWriter, r *http.Request, id, projectID uuid.UUID) (store.Provider, error) {
	var request struct {
		Name   string       `json:"name"`
		URL    string       `json:"url"`
		APIKey *secretField `json:"api_key"`
	}
	if err := decode(w, r, &request); err != nil {
		return store.Provider{}, err
	}
	if request.Name == "" {
		return store.Provider{}, invalid("name is required")
	}
	read, err := a.providerAt(r, id, projectID, request.URL, request.APIKey)
	read.Name = request.Name
	return read, err
}

// providerAt validates the URL and the key of the provider with the ID, and
// returns the provider without a name. A key given by value is encrypted for
// that provider. A key in a secret store is read once, so that an unreadable
// key is reported at once rather than during a turn.
func (a *api) providerAt(r *http.Request, id, projectID uuid.UUID, address string, key *secretField) (store.Provider, error) {
	if err := endpoint(address); err != nil {
		return store.Provider{}, err
	}
	read := store.Provider{ID: id, URL: address}
	switch {
	case key == nil:
	case key.Value != "" && key.Store == "" && key.Path == "" && key.Key == "":
		var err error
		if read.APIKey, err = a.keys.Seal(key.Value, store.ProviderKeyPlace(id)); err != nil {
			return store.Provider{}, err
		}
	case key.Value == "" && key.Store != "" && key.Path != "" && key.Key != "":
		at, err := a.keyInStore(r, projectID, *key)
		if err != nil {
			return store.Provider{}, err
		}
		read.APIKeyAt = &at
	default:
		return store.Provider{}, invalid(`api_key must be {"value": "…"} for a key that Aviary stores, or {"store": "…", "path": "…", "key": "…"} ` +
			`for a key in a secret store of the project; omit it for an endpoint that takes no key`)
	}
	return read, nil
}

// keyInStore checks that the key is in a secret store of the project and
// can be read.
func (a *api) keyInStore(r *http.Request, projectID uuid.UUID, key secretField) (store.SecretReference, error) {
	id, err := uuid.Parse(key.Store)
	if err != nil {
		return store.SecretReference{}, invalid("api_key.store must be the ID of a secret store of the project")
	}
	in, err := a.store.SecretStore(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && in.ProjectID != projectID):
		return store.SecretReference{}, invalid("api_key.store must be the ID of a secret store of the project")
	case err != nil:
		return store.SecretReference{}, err
	}
	credential, err := a.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
	if err != nil {
		return store.SecretReference{}, err
	}
	if _, err := a.stores.Read(r.Context(), in, credential, key.Path, key.Key); err != nil {
		return store.SecretReference{}, invalid("cannot read api_key from the secret store: " + err.Error())
	}
	return store.SecretReference{Store: id, Path: key.Path, Key: key.Key}, nil
}

// endpoint validates the base URL of a model provider.
func endpoint(address string) error {
	parsed, err := url.Parse(address)
	switch {
	case err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "":
		return invalid("url must be a URL that begins with http:// or https://")
	case parsed.User != nil:
		return invalid("url must not contain a user name or password; set the endpoint's key in api_key")
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
	read, err := a.readProvider(w, r, id, projectID)
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

// proxyReply is the header of the responses that the Egress Proxy makes
// itself: every other response is the answer of the model endpoint.
const proxyReply = "x-aviary-proxy"

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
// registered: the request gives the URL and the key of a provider, which is
// not stored.
func (a *api) endpointModels(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	var request struct {
		URL    string       `json:"url"`
		APIKey *secretField `json:"api_key"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	provider, err := a.providerAt(r, id, projectID, request.URL, request.APIKey)
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
// provider's key and through the Egress Proxy, as the model requests of a
// turn go: the answer also tells whether turns will reach the endpoint.
func (a *api) listModels(w http.ResponseWriter, r *http.Request, provider store.Provider) error {
	who := callerOf(r)
	token, err := authz.ListingToken(a.keys, authz.Listing{
		Provider: provider, UserID: who.id, Subject: who.Subject, Until: time.Now().Add(listTimeout),
	})
	if err != nil {
		return err
	}
	result, err := a.ask(r.Context(), provider.URL, token)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, result)
	return nil
}

// ask sends the model request of a listing, GET models under the URL of the
// provider, to the Egress Proxy with the listing's token, and returns what
// came of it.
func (a *api) ask(ctx context.Context, address, token string) (listed, error) {
	at, err := url.Parse(address)
	if err != nil {
		return listed{}, err
	}
	target := url.URL{Scheme: a.models.Scheme, Host: a.models.Host}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String()+strings.TrimSuffix(at.EscapedPath(), "/")+"/models", nil)
	if err != nil {
		return listed{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	began := time.Now()
	answer, err := a.proxy.Do(request)
	took := time.Since(began).Milliseconds()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return listed{Reason: "the endpoint did not answer in " + listTimeout.String(), DurationMS: took}, nil
	case errors.Is(err, context.Canceled):
		return listed{}, err
	case err != nil:
		// A turn would not reach the endpoint either.
		slog.ErrorContext(ctx, "ask the Egress Proxy for the models of a provider", "error", err)
		return listed{Reason: "the Egress Proxy did not answer", DurationMS: took}, nil
	}
	defer answer.Body.Close()
	if answer.Header.Get(proxyReply) != "" {
		why, err := io.ReadAll(io.LimitReader(answer.Body, 1024))
		if err != nil {
			return listed{}, err
		}
		return listed{Reason: strings.TrimSpace(string(why)), DurationMS: took}, nil
	}
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

// replaceProvider replaces the name, URL and key of a provider together, so
// that a stored key cannot be redirected to another URL.
func (a *api) replaceProvider(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "provider")
	if err != nil {
		return err
	}
	existing, err := a.store.Provider(r.Context(), id)
	if err != nil {
		return err
	}
	read, err := a.readProvider(w, r, id, existing.ProjectID)
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
