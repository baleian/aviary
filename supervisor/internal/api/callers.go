package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"aviary/supervisor/identity"
)

// callerKey is the key of the caller of a request in the context of the
// request.
type callerKey struct{}

var errNoCaller = &apiError{status: http.StatusUnauthorized, Code: "unauthenticated", Message: "the request needs a token of the identity provider of this installation"}

// identified passes on the requests whose caller the identity provider
// knows, each with its caller. A request tells who sends it with a token of
// the provider, in the header "Authorization: Bearer <token>". An
// installation without a provider asks nobody.
func (a *api) identified(next http.Handler) http.Handler {
	if a.callers == nil {
		return next
	}
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "Bearer") {
			return errNoCaller
		}
		caller, err := a.callers.Caller(r.Context(), strings.TrimSpace(token))
		if err != nil {
			slog.InfoContext(r.Context(), "a request was refused for its token", "method", r.Method, "path", r.URL.Path, "error", err)
			return errNoCaller
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
		return nil
	})
}

// me answers who sends the request, as the identity provider tells it. It
// is how to see what the installation reads from a token.
func (a *api) me(w http.ResponseWriter, r *http.Request) error {
	caller, _ := r.Context().Value(callerKey{}).(identity.Caller)
	respond(w, http.StatusOK, struct {
		Subject     string   `json:"subject,omitempty"`
		Username    string   `json:"username,omitempty"`
		DisplayName string   `json:"display_name,omitempty"`
		Groups      []string `json:"groups,omitempty"`
	}{caller.Subject, caller.Username, caller.DisplayName, caller.Groups})
	return nil
}
