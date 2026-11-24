package depot

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// The outcomes of a request for an object.
const (
	// Done is a request that was served.
	Done = "done"
	// Refused is a request that could not be served as it was made: its key
	// is invalid, has an object already or has none, or the Supervisor ended
	// the transfer early.
	Refused = "refused"
	// Failed is a request that the depot or its volume failed.
	Failed = "failed"
)

// written is the answer to a request that wrote an object.
type written struct {
	Size int64 `json:"size"`
}

// Handler serves the objects of the directory to the Supervisors:
//
//	PUT    /objects/{key}  writes the object of a new key
//	GET    /objects/{key}  reads an object
//	DELETE /objects/{key}  deletes an object and every object under the key
//
// served, if not nil, is called after each of these requests with its method
// and outcome, how long it took and the bytes of the object that were written
// or read.
func Handler(objects *Directory, served func(method, outcome string, took time.Duration, moved int64)) http.Handler {
	observed := func(serve func(http.ResponseWriter, *http.Request) (string, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			began := time.Now()
			outcome, moved := serve(w, r)
			if served != nil {
				served(r.Method, outcome, time.Since(began), moved)
			}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("PUT /objects/{key...}", observed(func(w http.ResponseWriter, r *http.Request) (string, int64) {
		body := &sent{Reader: r.Body}
		size, err := objects.Put(r.PathValue("key"), body)
		switch {
		case err == nil:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(written{Size: size})
			return Done, size
		case body.err != nil:
			return refuse(w, r, http.StatusBadRequest, body.err), 0
		}
		return refuse(w, r, statusOf(err), err), 0
	}))
	mux.HandleFunc("GET /objects/{key...}", observed(func(w http.ResponseWriter, r *http.Request) (string, int64) {
		object, size, err := objects.Get(r.PathValue("key"))
		if err != nil {
			return refuse(w, r, statusOf(err), err), 0
		}
		defer object.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		// The length tells the Supervisor if the object was cut short.
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		moved, err := io.Copy(w, object)
		switch {
		case err == nil:
			return Done, moved
		case r.Context().Err() != nil:
			// The Supervisor stopped reading.
			return Refused, moved
		}
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return Failed, moved
	}))
	mux.HandleFunc("DELETE /objects/{key...}", observed(func(w http.ResponseWriter, r *http.Request) (string, int64) {
		if err := objects.Delete(r.PathValue("key")); err != nil {
			return refuse(w, r, statusOf(err), err), 0
		}
		w.WriteHeader(http.StatusNoContent)
		return Done, 0
	}))
	return mux
}

// sent is the body of a request, and keeps the error that ended it early.
type sent struct {
	io.Reader
	err error
}

func (s *sent) Read(into []byte) (int, error) {
	n, err := s.Reader.Read(into)
	if err != nil && err != io.EOF {
		s.err = err
	}
	return n, err
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, ErrKey):
		return http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrExists):
		return http.StatusConflict
	case errors.Is(err, ErrFull):
		return http.StatusInsufficientStorage
	}
	return http.StatusInternalServerError
}

// refuse answers with the status and returns the outcome. What the depot or
// its volume failed goes to the log, and the answer names only the status.
func refuse(w http.ResponseWriter, r *http.Request, status int, err error) string {
	if status < http.StatusInternalServerError {
		http.Error(w, err.Error(), status)
		return Refused
	}
	slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	http.Error(w, http.StatusText(status), status)
	return Failed
}
