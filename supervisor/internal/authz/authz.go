// Package authz implements the Envoy external authorization service of the
// Egress Proxy. It identifies the turn that a request from a sandbox belongs
// to by the session token that the request carries.
package authz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/google/uuid"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// Headers that tell the Egress Proxy's configuration where to route a model
// request: the provider's host, and "true" if it uses TLS.
const (
	upstreamHeader    = "x-aviary-upstream"
	upstreamTLSHeader = "x-aviary-upstream-tls"
)

// place is what session tokens are sealed for. A value sealed for another
// place does not open as a token.
const place = "the token of a session"

// Token returns the session's token: its ID, encrypted with the keys of the
// installation. The sandbox sends it with every outbound request. Only
// Aviary can create or read it.
func Token(keys *secret.Keys, session uuid.UUID) (string, error) {
	sealed, err := keys.Seal(session.String(), place)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Trial is a model request with which the API tries a model provider before
// turns use it. It goes through the Egress Proxy as the model request of a
// turn does.
type Trial struct {
	// Provider is the provider to try: its ID, project, URL and key. It
	// need not be stored.
	Provider store.Provider
	// UserID and Subject identify the caller who tries it. They are zero if
	// requests to the API are not authenticated.
	UserID  uuid.UUID
	Subject string
	// Until is when the trial's token stops being accepted.
	Until time.Time
}

// trialPlace is what the tokens of trials are sealed for.
const trialPlace = "the trial of a model provider"

// sealedTrial is the form in which a trial is sealed into its token.
type sealedTrial struct {
	Project  uuid.UUID
	Provider uuid.UUID
	URL      string
	Key      []byte                 `json:",omitempty"`
	KeyAt    *store.SecretReference `json:",omitempty"`
	User     uuid.UUID
	Subject  string
	Until    time.Time
}

// TrialToken returns the token of a trial, which its model request carries
// in place of the token of a session: the trial, encrypted with the keys of
// the installation.
func TrialToken(keys *secret.Keys, trial Trial) (string, error) {
	encoded, err := json.Marshal(sealedTrial{
		Project: trial.Provider.ProjectID, Provider: trial.Provider.ID, URL: trial.Provider.URL,
		Key: trial.Provider.APIKey, KeyAt: trial.Provider.APIKeyAt,
		User: trial.UserID, Subject: trial.Subject, Until: trial.Until,
	})
	if err != nil {
		return "", err
	}
	token, err := keys.Seal(string(encoded), trialPlace)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

type authorizer struct {
	authv3.UnimplementedAuthorizationServer
	store store.Store
	// keys decrypt session tokens and stored API keys.
	keys *secret.Keys
	// stores reads API keys kept in the projects' secret stores.
	stores *secret.Stores
	// localhost, if not empty, is the host that the proxy reaches in place
	// of the local host of a provider's URL.
	localhost string
}

// New returns the authorization service. The local host of a provider's URL
// would be the proxy itself, so localhost names the host that stands in for
// it. If it is empty, none does.
func New(st store.Store, keys *secret.Keys, stores *secret.Stores, localhost string) authv3.AuthorizationServer {
	return &authorizer{store: st, keys: keys, stores: stores, localhost: localhost}
}

// Check allows a request that carries the token of a session with a running
// turn. It returns, as dynamic metadata for the proxy's access log, the IDs of
// the turn, session, agent and project, the provider of a model request, and,
// if requests to the API are authenticated, the ID and subject of the user
// who sent the turn's message. The subject identifies the user without
// Aviary's records.
//
// A model request carries the token as its bearer token, in place of an API
// key. A forward proxy request carries it as the proxy password. The context
// extension "listener" tells them apart.
//
// A model request is routed to the endpoint of the turn's provider, with the
// provider's API key. A forward proxy request goes where the pod sends it,
// and never gets a provider's key.
//
// A model request may carry the token of a trial instead. It is routed to
// the endpoint of the trial's provider, and its metadata names the project
// and the caller of the trial.
func (a *authorizer) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	forward := request.GetAttributes().GetContextExtensions()["listener"] == "forward"
	token := bearerToken(headers["authorization"])
	if forward {
		token = proxyPassword(headers["proxy-authorization"])
	}
	origin, found, err := a.origin(ctx, token, forward)
	if err != nil {
		// The proxy denies a request whose check fails.
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		slog.ErrorContext(ctx, "look up the origin of a request", "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !found {
		return denied(forward), nil
	}
	from := map[string]any{"project": origin.ProjectID.String()}
	if origin.TurnID != uuid.Nil {
		from["turn"], from["session"], from["agent"] = origin.TurnID.String(), origin.SessionID.String(), origin.AgentID.String()
		if !forward {
			from["provider"] = origin.Provider.ID.String()
		}
	}
	if origin.UserID != uuid.Nil {
		from["user"], from["subject"] = origin.UserID.String(), origin.Subject
	}
	metadata, err := structpb.NewStruct(from)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	allowed := &authv3.CheckResponse{
		Status:          &rpcstatus.Status{Code: int32(codes.OK)},
		DynamicMetadata: metadata,
	}
	if forward {
		return allowed, nil
	}
	endpoint, refusal := a.endpoint(ctx, origin, request.GetAttributes().GetRequest().GetHttp().GetPath())
	if refusal != nil {
		return refusal, nil
	}
	allowed.HttpResponse = &authv3.CheckResponse_OkResponse{OkResponse: endpoint}
	return allowed, nil
}

// origin returns where the request with the token comes from: the running
// turn of the session of a session token or, for a model request, the trial
// of a trial token. It returns false for any other token.
func (a *authorizer) origin(ctx context.Context, token string, forward bool) (store.Origin, bool, error) {
	if session, known := a.session(token); known {
		origin, err := a.store.SessionTurn(ctx, session)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return store.Origin{}, false, nil
		case err != nil:
			return store.Origin{}, false, fmt.Errorf("running turn of session %s: %w", session, err)
		}
		return origin, true, nil
	}
	trial, known := a.trial(token)
	if !known || forward {
		return store.Origin{}, false, nil
	}
	origin := store.Origin{ProjectID: trial.Provider.ProjectID, UserID: trial.UserID, Subject: trial.Subject, Provider: trial.Provider}
	if at := trial.Provider.APIKeyAt; at != nil {
		in, err := a.store.SecretStore(ctx, at.Store)
		if errors.Is(err, store.ErrNotFound) || (err == nil && in.ProjectID != origin.ProjectID) {
			return store.Origin{}, false, nil
		}
		if err != nil {
			return store.Origin{}, false, fmt.Errorf("secret store %s of a trial: %w", at.Store, err)
		}
		origin.SecretStore = &in
	}
	return origin, true, nil
}

// endpoint returns how the proxy forwards a model request for path: to the
// endpoint of the turn's provider, with the provider's API key in place of
// the session token. A provider without a key gets no Authorization header.
// A path outside the path of the provider's URL, or a key that cannot be
// read, is refused.
func (a *authorizer) endpoint(ctx context.Context, origin store.Origin, path string) (*authv3.OkHttpResponse, *authv3.CheckResponse) {
	provider := origin.Provider
	address, err := url.Parse(provider.URL)
	if err != nil {
		slog.ErrorContext(ctx, "parse provider URL", "provider", provider.ID, "error", err)
		return nil, refused(typev3.StatusCode_ServiceUnavailable, "the model provider's URL is invalid\n")
	}
	if !under(path, address.Path) {
		return nil, refused(typev3.StatusCode_NotFound, "model requests must be under the path "+address.Path+"\n")
	}
	set := func(name, value string) *corev3.HeaderValueOption {
		return &corev3.HeaderValueOption{
			Header:       &corev3.HeaderValue{Key: name, Value: value},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		}
	}
	endpoint := &authv3.OkHttpResponse{Headers: []*corev3.HeaderValueOption{
		set(upstreamHeader, a.upstream(address)),
		set(upstreamTLSHeader, strconv.FormatBool(address.Scheme == "https")),
	}}
	var key string
	switch in := origin.SecretStore; {
	case provider.APIKeyAt != nil && in != nil:
		credential, err := a.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
		if err != nil {
			slog.ErrorContext(ctx, "decrypt secret store credential", "store", in.ID, "error", err)
			return nil, refused(typev3.StatusCode_ServiceUnavailable, "cannot read the credential of the model provider's secret store\n")
		}
		// The pod is told why the project's own store cannot be read. The
		// error holds nothing of the key.
		if key, err = a.stores.Read(ctx, *in, credential, provider.APIKeyAt.Path, provider.APIKeyAt.Key); err != nil {
			slog.WarnContext(ctx, "read provider API key from secret store", "provider", provider.ID, "store", in.ID, "error", err)
			return nil, refused(typev3.StatusCode_ServiceUnavailable,
				"cannot read the model provider's API key from its secret store: "+err.Error()+"\n")
		}
	case provider.APIKey != nil:
		if key, err = a.keys.Open(provider.APIKey, store.ProviderKeyPlace(provider.ID)); err != nil {
			slog.ErrorContext(ctx, "decrypt provider API key", "provider", provider.ID, "error", err)
			return nil, refused(typev3.StatusCode_ServiceUnavailable, "cannot read the model provider's API key\n")
		}
	default:
		endpoint.HeadersToRemove = []string{"authorization"}
		return endpoint, nil
	}
	endpoint.Headers = append(endpoint.Headers, set("authorization", "Bearer "+key))
	return endpoint, nil
}

// upstream returns the host that the proxy connects to for a provider at the
// address, with the port of the address if it has one: the host of the
// address, or what stands in for it if it is the local host.
func (a *authorizer) upstream(address *url.URL) string {
	if a.localhost == "" || !local(address.Hostname()) {
		return address.Host
	}
	host := a.localhost
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := address.Port(); port != "" {
		host += ":" + port
	}
	return host
}

// local reports whether the host is the local host: by that name, or as a
// loopback address.
func local(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// under reports whether the request path, which may include a query, is the
// endpoint path or below it. A path with dot segments is not, since how it
// resolves is up to the endpoint.
func under(path, endpoint string) bool {
	path, _, _ = strings.Cut(path, "?")
	for _, step := range strings.Split(path, "/") {
		if plain, err := url.PathUnescape(step); err != nil || plain == "." || plain == ".." {
			return false
		}
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	return path == endpoint || strings.HasPrefix(path, endpoint+"/")
}

// refused denies a request with the status, and with why as the body.
func refused(status typev3.StatusCode, why string) *authv3.CheckResponse {
	return &authv3.CheckResponse{
		Status: &rpcstatus.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: &authv3.DeniedHttpResponse{
			Status: &typev3.HttpStatus{Code: status},
			Body:   why,
		}},
	}
}

// session returns the session of the token, or false if it is not a session
// token.
func (a *authorizer) session(token string) (uuid.UUID, bool) {
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return uuid.Nil, false
	}
	opened, err := a.keys.Open(sealed, place)
	if err != nil {
		return uuid.Nil, false
	}
	session, err := uuid.Parse(opened)
	return session, err == nil
}

// trial returns the trial of the token, or false if it is not the token of
// a trial that is still accepted.
func (a *authorizer) trial(token string) (Trial, bool) {
	encrypted, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return Trial{}, false
	}
	opened, err := a.keys.Open(encrypted, trialPlace)
	if err != nil {
		return Trial{}, false
	}
	var of sealedTrial
	if err := json.Unmarshal([]byte(opened), &of); err != nil || !time.Now().Before(of.Until) {
		return Trial{}, false
	}
	return Trial{
		Provider: store.Provider{ID: of.Provider, ProjectID: of.Project, URL: of.URL, APIKey: of.Key, APIKeyAt: of.KeyAt},
		UserID:   of.User, Subject: of.Subject, Until: of.Until,
	}, true
}

// bearerToken returns the token of an Authorization header.
func bearerToken(header string) string {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return ""
	}
	return token
}

// proxyPassword returns the password of a Proxy-Authorization header. The
// user name can be anything.
func proxyPassword(header string) string {
	encoded, found := strings.CutPrefix(header, "Basic ")
	if !found {
		return ""
	}
	credentials, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	_, password, _ := strings.Cut(string(credentials), ":")
	return password
}

// denied denies a request. For the forward proxy it asks for credentials,
// since some clients, such as git, send them only when asked.
func denied(forward bool) *authv3.CheckResponse {
	response := &authv3.DeniedHttpResponse{
		Status: &typev3.HttpStatus{Code: typev3.StatusCode_Unauthorized},
		Body:   "unknown token, or no turn is running in its session\n",
	}
	if forward {
		response.Status.Code = typev3.StatusCode_ProxyAuthenticationRequired
		response.Headers = []*corev3.HeaderValueOption{{
			Header: &corev3.HeaderValue{Key: "Proxy-Authenticate", Value: `Basic realm="aviary"`},
		}}
	}
	return &authv3.CheckResponse{
		Status:       &rpcstatus.Status{Code: int32(codes.Unauthenticated)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: response},
	}
}
