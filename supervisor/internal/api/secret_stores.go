package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// secretStore is a secret store as the API answers it. What the store is
// logged in to with is not in the answer: only that it is stored.
type secretStore struct {
	ID         uuid.UUID       `json:"id"`
	ProjectID  uuid.UUID       `json:"project_id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Settings   json.RawMessage `json:"settings"`
	Credential stored          `json:"credential"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func answeredStore(from store.SecretStore) secretStore {
	return secretStore{
		ID: from.ID, ProjectID: from.ProjectID, Name: from.Name, Kind: from.Kind, Settings: from.Settings,
		Credential: stored{Stored: true}, CreatedAt: from.CreatedAt, UpdatedAt: from.UpdatedAt,
	}
}

// readSecretStore reads what a secret store is created with or replaced
// with: all that makes the store with the ID. Its kind says how the store is
// asked, and its settings are what that kind needs to know of it. The store
// is tried before it is taken, and its credential is sealed for that store.
func (a *api) readSecretStore(w http.ResponseWriter, r *http.Request, id uuid.UUID) (store.SecretStore, error) {
	var request struct {
		Name       string          `json:"name"`
		Kind       string          `json:"kind"`
		Settings   json.RawMessage `json:"settings"`
		Credential *secretValue    `json:"credential"`
	}
	if err := decode(w, r, &request); err != nil {
		return store.SecretStore{}, err
	}
	switch {
	case request.Name == "":
		return store.SecretStore{}, invalid("name is required")
	case request.Credential == nil || request.Credential.Value == "":
		return store.SecretStore{}, invalid("credential.value is required: what the store is logged in to with")
	}
	if err := a.stores.Check(r.Context(), request.Kind, request.Settings, request.Credential.Value); err != nil {
		return store.SecretStore{}, invalid(err.Error())
	}
	credential, err := a.keys.Seal(request.Credential.Value, store.SecretStoreCredentialPlace(id))
	if err != nil {
		return store.SecretStore{}, err
	}
	return store.SecretStore{ID: id, Name: request.Name, Kind: request.Kind, Settings: request.Settings, Credential: credential}, nil
}

func (a *api) createSecretStore(w http.ResponseWriter, r *http.Request) error {
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
	read, err := a.readSecretStore(w, r, id)
	if err != nil {
		return err
	}
	read.ProjectID = projectID
	created, err := a.store.CreateSecretStore(r.Context(), read)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, answeredStore(created))
	return nil
}

func (a *api) getSecretStore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "store")
	if err != nil {
		return err
	}
	found, err := a.store.SecretStore(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, answeredStore(found))
	return nil
}

// replaceSecretStore gives a secret store another name, kind and settings,
// all at once and with its credential: nobody can have a credential that is
// stored sent to another address.
func (a *api) replaceSecretStore(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "store")
	if err != nil {
		return err
	}
	if _, err := a.store.SecretStore(r.Context(), id); err != nil {
		return err
	}
	read, err := a.readSecretStore(w, r, id)
	if err != nil {
		return err
	}
	replaced, err := a.store.ReplaceSecretStore(r.Context(), read)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, answeredStore(replaced))
	return nil
}
