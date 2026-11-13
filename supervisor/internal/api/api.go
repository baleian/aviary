// Package api serves the Supervisor's public HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"aviary/supervisor/internal/store"
)

const maxBodyBytes = 1 << 20

type api struct {
	store *store.Store
}

type apiError struct {
	status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return e.Message }

var (
	errNotFound = &apiError{http.StatusNotFound, "not_found", "no such resource"}
	errInternal = &apiError{http.StatusInternalServerError, "internal", "internal error"}
)

func New(st *store.Store) http.Handler {
	a := &api{store: st}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/projects", handler(a.createProject))
	mux.Handle("GET /v1/projects/{pid}", handler(a.getProject))
	return mux
}

func (a *api) createProject(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Name == "" {
		return invalid("name is required")
	}
	project, err := a.store.CreateProject(r.Context(), request.Name)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, project)
	return nil
}

func (a *api) getProject(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		return errNotFound
	}
	project, err := a.store.Project(r.Context(), id)
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, project)
	return nil
}

// handler turns the error a handler returns into the error response.
type handler func(w http.ResponseWriter, r *http.Request) error

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := h(w, r)
	if err == nil {
		return
	}
	var failure *apiError
	switch {
	case errors.As(err, &failure):
	case errors.Is(err, store.ErrNotFound):
		failure = errNotFound
	default:
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		failure = errInternal
	}
	respond(w, failure.status, failure)
}

func invalid(message string) error {
	return &apiError{http.StatusBadRequest, "invalid_request", message}
}

func decode(w http.ResponseWriter, r *http.Request, into any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return invalid("request body is not the JSON this endpoint expects")
	}
	return nil
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, so a failed write can only be logged.
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}
