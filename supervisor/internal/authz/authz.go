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
	"golang.org/x/net/http/httpguts"
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

// Listing is the model request with which the API asks a model provider for
// its models. It goes through the Egress Proxy as the model request of a turn
// does.
type Listing struct {
	// Provider is the provider that is asked: its ID, project, URL, headers
	// and key. It need not be stored.
	Provider store.Provider
	// UserID and Subject identify the caller who asks. They are zero if
	// requests to the API are not authenticated.
	UserID  uuid.UUID
	Subject string
	// Until is when the listing's token stops being accepted.
	Until time.Time
}

// listingPlace is what the tokens of listings are sealed for.
const listingPlace = "the listing of a provider's models"

// sealedListing is the form in which a listing is sealed into its token.
type sealedListing struct {
	Project  uuid.UUID
	Provider uuid.UUID
	URL      string
	Headers  []store.Header         `json:",omitempty"`
	Key      []byte                 `json:",omitempty"`
	KeyAt    *store.SecretReference `json:",omitempty"`
	User     uuid.UUID
	Subject  string
	Until    time.Time
}

// ListingToken returns the token of a listing, which its model request
// carries in place of the token of a session: the listing, encrypted with
// the keys of the installation.
func ListingToken(keys *secret.Keys, listing Listing) (string, error) {
	encoded, err := json.Marshal(sealedListing{
		Project: listing.Provider.ProjectID, Provider: listing.Provider.ID, URL: listing.Provider.URL,
		Headers: listing.Provider.Headers, Key: listing.Provider.APIKey, KeyAt: listing.Provider.APIKeyAt,
		User: listing.UserID, Subject: listing.Subject, Until: listing.Until,
	})
	if err != nil {
		return "", err
	}
	token, err := keys.Seal(string(encoded), listingPlace)
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
// provider's headers and API key. A forward proxy request goes where the pod
// sends it, and never gets a provider's headers or key.
//
// A model request may carry the token of a listing instead. It is routed to
// the endpoint of the listing's provider, and its metadata names the project
// and the caller of the listing.
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
	stores, err := a.storesOf(ctx, origin)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		slog.ErrorContext(ctx, "look up the secret stores of a provider", "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	endpoint, refusal := a.endpoint(ctx, origin, stores, request.GetAttributes().GetRequest().GetHttp().GetPath())
	if refusal != nil {
		return refusal, nil
	}
	allowed.HttpResponse = &authv3.CheckResponse_OkResponse{OkResponse: endpoint}
	return allowed, nil
}

// origin returns where the request with the token comes from: the running
// turn of the session of a session token or, for a model request, the
// listing of a listing token. It returns false for any other token.
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
	listing, known := a.listing(token)
	if !known || forward {
		return store.Origin{}, false, nil
	}
	origin := store.Origin{ProjectID: listing.Provider.ProjectID, UserID: listing.UserID, Subject: listing.Subject, Provider: listing.Provider}
	if at := listing.Provider.APIKeyAt; at != nil {
		in, err := a.store.SecretStore(ctx, at.Store)
		if errors.Is(err, store.ErrNotFound) || (err == nil && in.ProjectID != origin.ProjectID) {
			return store.Origin{}, false, nil
		}
		if err != nil {
			return store.Origin{}, false, fmt.Errorf("secret store %s of a listing: %w", at.Store, err)
		}
		origin.SecretStore = &in
	}
	return origin, true, nil
}

// storesOf returns the secret stores of the project that keep the secrets of
// the origin's provider, by ID: the store of its key, which the origin has,
// and those of its headers.
func (a *authorizer) storesOf(ctx context.Context, origin store.Origin) (map[uuid.UUID]store.SecretStore, error) {
	stores := map[uuid.UUID]store.SecretStore{}
	if in := origin.SecretStore; in != nil {
		stores[in.ID] = *in
	}
	for _, header := range origin.Provider.Headers {
		if header.At == nil {
			continue
		}
		if _, known := stores[header.At.Store]; known {
			continue
		}
		in, err := a.store.SecretStore(ctx, header.At.Store)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue
		case err != nil:
			return nil, fmt.Errorf("secret store %s of a header: %w", header.At.Store, err)
		}
		if in.ProjectID == origin.ProjectID {
			stores[in.ID] = in
		}
	}
	return stores, nil
}

// endpoint returns how the proxy forwards a model request for path: to the
// endpoint of the turn's provider, with the provider's headers, and with its
// API key in place of the session token. A header of the provider replaces
// one that the request has by the same name. A provider without a key gets no
// Authorization header. A path outside the path of the provider's URL, or a
// secret that cannot be read, is refused. stores are the secret stores of
// the provider's secrets.
func (a *authorizer) endpoint(ctx context.Context, origin store.Origin, stores map[uuid.UUID]store.SecretStore, path string) (*authv3.OkHttpResponse, *authv3.CheckResponse) {
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
	for _, header := range provider.Headers {
		value, refusal := a.valueOf(ctx, provider, stores, header)
		if refusal != nil {
			return nil, refusal
		}
		endpoint.Headers = append(endpoint.Headers, set(header.Name, value))
	}
	var key string
	switch in := origin.SecretStore; {
	case provider.APIKeyAt != nil && in != nil:
		var refusal *authv3.CheckResponse
		if key, refusal = a.read(ctx, *in, *provider.APIKeyAt, "the model provider's API key"); refusal != nil {
			return nil, refusal
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

// valueOf returns the value of a header of the provider: the value that it
// has, its stored secret, or its secret from one of the stores. A secret that
// cannot be read refuses the request.
func (a *authorizer) valueOf(ctx context.Context, provider store.Provider, stores map[uuid.UUID]store.SecretStore, header store.Header) (string, *authv3.CheckResponse) {
	what := "the model provider's header " + header.Name
	switch {
	case header.At != nil:
		in, found := stores[header.At.Store]
		if !found {
			return "", refused(typev3.StatusCode_ServiceUnavailable, "the secret store of "+what+" is not in the project\n")
		}
		value, refusal := a.read(ctx, in, *header.At, what)
		if refusal != nil {
			return "", refusal
		}
		// A secret that a store holds may end with a line break.
		if value = strings.TrimSpace(value); value == "" || !httpguts.ValidHeaderFieldValue(value) {
			return "", refused(typev3.StatusCode_ServiceUnavailable, "the secret of "+what+" in its secret store is not the value of an HTTP header\n")
		}
		return value, nil
	case header.Sealed != nil:
		value, err := a.keys.Open(header.Sealed, store.ProviderHeaderPlace(provider.ID, header.Name))
		if err != nil {
			slog.ErrorContext(ctx, "decrypt provider header", "provider", provider.ID, "header", header.Name, "error", err)
			return "", refused(typev3.StatusCode_ServiceUnavailable, "cannot read "+what+"\n")
		}
		return value, nil
	}
	return header.Value, nil
}

// read returns a secret from a secret store, or the refusal of the request
// that needs it, which says why: the pod is told why the project's own store
// cannot be read, and the error holds nothing of the secret. what names the
// secret.
func (a *authorizer) read(ctx context.Context, in store.SecretStore, at store.SecretReference, what string) (string, *authv3.CheckResponse) {
	credential, err := a.keys.Open(in.Credential, store.SecretStoreCredentialPlace(in.ID))
	if err != nil {
		slog.ErrorContext(ctx, "decrypt secret store credential", "store", in.ID, "error", err)
		return "", refused(typev3.StatusCode_ServiceUnavailable, "cannot read the credential of the model provider's secret store\n")
	}
	value, err := a.stores.Read(ctx, in, credential, at.Path, at.Key)
	if err != nil {
		slog.WarnContext(ctx, "read a secret of a model provider from its secret store", "secret", what, "store", in.ID, "error", err)
		return "", refused(typev3.StatusCode_ServiceUnavailable, "cannot read "+what+" from its secret store: "+err.Error()+"\n")
	}
	return value, nil
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

// listing returns the listing of the token, or false if it is not the token
// of a listing that is still accepted.
func (a *authorizer) listing(token string) (Listing, bool) {
	encrypted, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || token == "" {
		return Listing{}, false
	}
	opened, err := a.keys.Open(encrypted, listingPlace)
	if err != nil {
		return Listing{}, false
	}
	var of sealedListing
	if err := json.Unmarshal([]byte(opened), &of); err != nil || !time.Now().Before(of.Until) {
		return Listing{}, false
	}
	return Listing{
		Provider: store.Provider{
			ID: of.Provider, ProjectID: of.Project, URL: of.URL, Headers: of.Headers, APIKey: of.Key, APIKeyAt: of.KeyAt,
		},
		UserID: of.User, Subject: of.Subject, Until: of.Until,
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
