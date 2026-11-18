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

// Callers is who may use the API of an installation.
type Callers struct {
	// Provider tells who sends a request. An installation without an
	// identity provider has none: nobody is asked, and every request is an
	// administrator's.
	Provider identity.Provider
	// Admins are the groups of the provider whose members are the
	// administrators of the installation.
	Admins []string
	// Users are the groups whose members may use the API, as the
	// administrators may. If there are none, everyone whom the provider
	// knows may.
	Users []string
}

// caller is who sends a request, and what the installation makes of them.
type caller struct {
	identity.Caller
	// id is the ID of the caller as a user of the installation. A caller
	// that is not asked who they are is no user, and has none.
	id uuid.UUID
	// admin says that the caller is an administrator of the installation.
	admin bool
}

// callerKey is the key of the caller of a request in the context of the
// request.
type callerKey struct{}

func callerOf(r *http.Request) caller {
	who, _ := r.Context().Value(callerKey{}).(caller)
	return who
}

var (
	errNoCaller   = &apiError{status: http.StatusUnauthorized, Code: "unauthenticated", Message: "the request needs a token of the identity provider of this installation"}
	errNotAllowed = &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "the caller is in none of the groups that may use this installation"}
)

// identified passes on the requests whose caller the identity provider
// knows, each with its caller. A request tells who sends it with a token of
// the provider, in the header "Authorization: Bearer <token>", and is a user
// of the installation from the first request on. An installation without a
// provider asks nobody.
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
				slog.InfoContext(r.Context(), "a request was refused for its token", "method", r.Method, "path", r.URL.Path, "error", err)
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

// allowed passes on the requests of the callers that may use the API: the
// administrators and the members of the groups of users, or everyone if the
// installation names no such group.
func (a *api) allowed(next http.Handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		if who := callerOf(r); !who.admin && len(a.callers.Users) > 0 && !among(who.Groups, a.callers.Users) {
			return errNotAllowed
		}
		next.ServeHTTP(w, r)
		return nil
	})
}

// among says whether one of the groups is among those that are named.
func among(groups, named []string) bool {
	return slices.ContainsFunc(groups, func(group string) bool { return slices.Contains(named, group) })
}

// me answers who sends the request: the ID of the caller as a user of the
// installation, what the identity provider tells of the caller, and whether
// the installation takes the caller for an administrator. It is how to see
// what the installation reads from a token, also for a caller that may not
// use the rest of the API.
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
