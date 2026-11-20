package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"aviary/supervisor/identity"
)

// Callers configures who may use the API.
type Callers struct {
	// Provider authenticates requests. If it is nil, requests are not
	// authenticated and every caller is an admin.
	Provider identity.Provider
	// Admins are the groups whose members are admins.
	Admins []string
	// Users are the groups whose members, besides the admins, may use the
	// API. If empty, every authenticated caller may.
	Users []string
}

// caller is the sender of a request.
type caller struct {
	identity.Caller
	// id is the caller's user ID, or uuid.Nil if requests are not
	// authenticated.
	id    uuid.UUID
	admin bool
}

// callerKey is the context key of the request's caller.
type callerKey struct{}

func callerOf(r *http.Request) caller {
	who, _ := r.Context().Value(callerKey{}).(caller)
	return who
}

var (
	errNoCaller   = &apiError{status: http.StatusUnauthorized, Code: "unauthenticated", Message: "a valid bearer token from this installation's identity provider is required"}
	errNotAllowed = &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "the caller is not in any group that may use this installation"}
)

// identified authenticates the bearer token of a request with the identity
// provider, and passes the request on with its caller in the context. A
// caller's user record is created on its first request. Without a provider,
// every request is passed on as an admin's.
func (a *api) identified(next http.Handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		who := caller{admin: true}
		if a.callers.Provider != nil {
			scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			if !strings.EqualFold(scheme, "Bearer") {
				return errNoCaller
			}
			told, err := a.callers.Provider.Caller(r.Context(), strings.TrimSpace(token))
			if err != nil {
				slog.InfoContext(r.Context(), "authentication failed", "method", r.Method, "path", r.URL.Path, "error", err)
				return errNoCaller
			}
			user, err := a.store.UserOf(r.Context(), told.Subject, told.Username, told.DisplayName)
			if err != nil {
				return err
			}
			who = caller{Caller: told, id: user.ID, admin: among(told.Groups, a.callers.Admins)}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, who)))
		return nil
	})
}

// allowed passes on the requests of admins and members of the Users groups,
// or of everyone if Users is empty.
func (a *api) allowed(next http.Handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		if who := callerOf(r); !who.admin && len(a.callers.Users) > 0 && !among(who.Groups, a.callers.Users) {
			return errNotAllowed
		}
		next.ServeHTTP(w, r)
		return nil
	})
}

// among reports whether any of groups is in named.
func among(groups, named []string) bool {
	return slices.ContainsFunc(groups, func(group string) bool { return slices.Contains(named, group) })
}

// me returns the caller's user ID, identity and admin status. It is open to
// callers that may not use the rest of the API, so that they can see how
// their token is read.
func (a *api) me(w http.ResponseWriter, r *http.Request) error {
	who := callerOf(r)
	var id string
	if who.id != uuid.Nil {
		id = who.id.String()
	}
	respond(w, http.StatusOK, struct {
		ID          string   `json:"id,omitempty"`
		Subject     string   `json:"subject,omitempty"`
		Username    string   `json:"username,omitempty"`
		DisplayName string   `json:"display_name,omitempty"`
		Groups      []string `json:"groups,omitempty"`
		Admin       bool     `json:"admin"`
	}{id, who.Subject, who.Username, who.DisplayName, who.Groups, who.admin})
	return nil
}
