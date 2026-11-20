// Package resolver answers the egress of the sandbox service, which asks it
// about every request that leaves a sandbox for a host that the egress is
// told to ask about. The resolver writes the record of the request: which
// turn sent it, and who sent the message of that turn. Into a request to
// the model endpoint of the turn's agent it has the egress put the key of
// the endpoint, which never enters the sandbox.
package resolver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// place is what the path of a sandbox is sealed for.
const place = "the path of a sandbox at the resolver"

// questionLimit is the most of a question that is read.
const questionLimit = 64 << 10

type Resolver struct {
	store  store.Store
	keys   *secret.Keys
	stores *secret.Stores
	// records is where the records of the requests are written.
	records *slog.Logger
}

// New returns a resolver that reads the turns from the store, opens the keys
// of the model endpoints with the keys of the installation or reads them
// from the secret stores, and writes the records of the requests to the
// logger.
func New(st store.Store, keys *secret.Keys, stores *secret.Stores, records *slog.Logger) *Resolver {
	return &Resolver{store: st, keys: keys, stores: stores, records: records}
}

// Endpoint returns where the egress of the sandbox is to ask the resolver
// that is at the address: under a path that Aviary seals for the sandbox.
// The resolver answers no question about the sandbox that comes by another
// path. The egress does not tell a sandbox where it asks, so a sandbox that
// reaches the resolver itself is answered nothing.
func (r *Resolver) Endpoint(address, sandbox string) (string, error) {
	sealed, err := r.keys.Seal(sandbox, place)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(address, "/") + "/" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// question is what the egress asks about a request.
type question struct {
	// Subject is the sandbox that the request comes from, as the egress
	// knows it.
	Subject     string `json:"subject"`
	Destination struct {
		Scheme    string `json:"scheme"`
		Authority string `json:"authority"`
	} `json:"destination"`
	Request struct {
		Method string `json:"method"`
		Host   string `json:"host"`
		// Path is the target of the request, with its query.
		Path string `json:"path"`
	} `json:"request"`
}

// Handler returns what serves the questions of the egress.
func (r *Resolver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{sealed}/resolve", r.resolve)
	return mux
}

func (r *Resolver) resolve(w http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	var asked question
	if err := json.NewDecoder(http.MaxBytesReader(w, request.Body, questionLimit)).Decode(&asked); err != nil || asked.Subject == "" {
		http.Error(w, "that is no question about a request", http.StatusBadRequest)
		return
	}
	if !r.sealedFor(request.PathValue("sealed"), asked.Subject) {
		http.NotFound(w, request)
		return
	}
	host, port := hostAndPort(asked.Destination.Scheme, asked.Request.Host, asked.Destination.Authority)
	// A query may carry what is not to be kept.
	path, _, _ := strings.Cut(asked.Request.Path, "?")
	record := []any{
		"sandbox", asked.Subject, "method", asked.Request.Method, "scheme", asked.Destination.Scheme,
		"host", host, "port", port, "path", path,
	}

	origin, err := r.store.SandboxTurn(ctx, asked.Subject)
	if errors.Is(err, store.ErrNotFound) {
		r.records.WarnContext(ctx, "request refused", append(record, "why", "no turn runs in the sandbox")...)
		http.Error(w, "no turn runs in the sandbox", http.StatusForbidden)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "find the turn of a sandbox", "sandbox", asked.Subject, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	record = append(record, "turn", origin.TurnID, "session", origin.SessionID, "agent", origin.AgentID, "project", origin.ProjectID)
	if origin.UserID != uuid.Nil {
		record = append(record, "user", origin.UserID, "subject", origin.Subject)
	}

	headers := map[string]string{}
	if provider := origin.Provider; provider != nil && toEndpoint(provider.URL, asked.Destination.Scheme, host, port, path) {
		record = append(record, "provider", provider.ID)
		key, err := r.key(ctx, origin)
		if err != nil {
			r.records.WarnContext(ctx, "request refused", append(record, "why", err.Error())...)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// An endpoint that takes no key is sent none.
		if key != "" {
			headers["authorization"] = "Bearer " + key
		}
	}
	r.records.InfoContext(ctx, "request", record...)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"headers": headers})
}

// sealedFor tells whether the path was sealed for the sandbox.
func (r *Resolver) sealedFor(path, sandbox string) bool {
	sealed, err := base64.RawURLEncoding.DecodeString(path)
	if err != nil {
		return false
	}
	opened, err := r.keys.Open(sealed, place)
	return err == nil && opened == sandbox
}

// key returns the key of the model endpoint of the turn's agent, which is
// nothing if the endpoint takes none.
func (r *Resolver) key(ctx context.Context, origin store.Origin) (string, error) {
	provider := origin.Provider
	switch in := origin.SecretStore; {
	case provider.APIKeyAt != nil && in != nil:
		// The key is in a secret store of the project, and is read from it.
		credential, err := r.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
		if err != nil {
			slog.ErrorContext(ctx, "open the credential of a secret store", "store", in.ID, "error", err)
			return "", errors.New("the credential of the secret store of the model provider cannot be read")
		}
		key, err := r.stores.Read(ctx, *in, credential, provider.APIKeyAt.Path, provider.APIKeyAt.Key)
		if err != nil {
			return "", fmt.Errorf("the API key of the model provider cannot be read from its secret store: %w", err)
		}
		return key, nil
	case provider.APIKey != nil:
		key, err := r.keys.Open(provider.APIKey, store.ProviderKeyPlace(provider.ID))
		if err != nil {
			slog.ErrorContext(ctx, "open the API key of a provider", "provider", provider.ID, "error", err)
			return "", errors.New("the API key of the model provider cannot be read")
		}
		return key, nil
	}
	return "", nil
}

// hostAndPort returns the host that a request is for, and the port that it
// goes to: the one that its host names, or else the one that it is sent
// to, or else the one of its scheme.
func hostAndPort(scheme, host, authority string) (string, string) {
	if name, port, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(name), port
	}
	if _, port, err := net.SplitHostPort(authority); err == nil {
		return strings.ToLower(host), port
	}
	return strings.ToLower(host), defaultPort(scheme)
}

func defaultPort(scheme string) string {
	if scheme == "http" {
		return "80"
	}
	return "443"
}

// toEndpoint tells whether a request goes to the model endpoint at the URL:
// to its scheme, host and port, at its path or below it. The key of an
// endpoint is for the endpoint alone.
func toEndpoint(endpoint, scheme, host, port, path string) bool {
	address, err := url.Parse(endpoint)
	if err != nil || address.Scheme != scheme || !strings.EqualFold(address.Hostname(), host) {
		return false
	}
	at := address.Port()
	if at == "" {
		at = defaultPort(address.Scheme)
	}
	return at == port && under(path, address.Path)
}

// under tells whether a path is that of an endpoint or below it. A path with
// a step back up is not: where it leads is for the endpoint to say.
func under(path, endpoint string) bool {
	for _, step := range strings.Split(path, "/") {
		if plain, err := url.PathUnescape(step); err != nil || plain == "." || plain == ".." {
			return false
		}
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	return path == endpoint || strings.HasPrefix(path, endpoint+"/")
}
