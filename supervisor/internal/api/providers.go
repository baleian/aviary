package api

import (
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// secretValue is a secret as a request gives it. The API takes a secret and
// never gives one back.
type secretValue struct {
	Value string `json:"value"`
}

// stored stands for a secret in what the API answers: it tells that the
// secret is stored and not what it is.
type stored struct {
	Stored bool `json:"stored"`
}

// provider is a model provider as the API answers it.
type provider struct {
	store.Provider
	APIKey *stored `json:"api_key,omitempty"`
}

func answered(from store.Provider) provider {
	answer := provider{Provider: from}
	if from.APIKey != nil {
		answer.APIKey = &stored{Stored: true}
	}
	return answer
}

// readProvider reads what a provider is created with or replaced with: all
// that makes the provider with the ID. The key is sealed for that provider.
func (a *api) readProvider(w http.ResponseWriter, r *http.Request, id uuid.UUID) (store.Provider, error) {
	var request struct {
		Name   string       `json:"name"`
		URL    string       `json:"url"`
		APIKey *secretValue `json:"api_key"`
	}
	if err := decode(w, r, &request); err != nil {
		return store.Provider{}, err
	}
	if request.Name == "" {
		return store.Provider{}, invalid("name is required")
	}
	if err := endpoint(request.URL); err != nil {
		return store.Provider{}, err
	}
	read := store.Provider{ID: id, Name: request.Name, URL: request.URL}
	if request.APIKey != nil {
		if request.APIKey.Value == "" {
			return store.Provider{}, invalid("api_key.value is required: an endpoint that takes no key has no api_key")
		}
		var err error
		if read.APIKey, err = a.keys.Seal(request.APIKey.Value, store.ProviderKeyPlace(id)); err != nil {
			return store.Provider{}, err
		}
	}
	return read, nil
}

// endpoint checks the base URL of a model endpoint.
func endpoint(address string) error {
	parsed, err := url.Parse(address)
	switch {
	case err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "":
		return invalid("url must be a URL that begins with http:// or https://")
	case parsed.User != nil:
		return invalid("url must hold no user name and no password: the key of the endpoint is its api_key")
	case parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "":
		return invalid("url must end with its path")
	}
	return nil
}

func (a *api) createProvider(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	read, err := a.readProvider(w, r, id)
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

// replaceProvider gives a provider another name, URL and key, all three at
// once. A request cannot change the URL and leave the key: nobody can have a
// key that is stored sent to another address.
func (a *api) replaceProvider(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "provider")
	if err != nil {
		return err
	}
	read, err := a.readProvider(w, r, id)
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
