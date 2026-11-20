package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"aviary/supervisor/store"
)

// Permissions of project members. A role is a set of them.
const (
	// projectView allows reading the project and its agents, model providers
	// and secret stores, without secrets. Every member has it.
	projectView = "project.view"
	// agentsManage allows creating agents.
	agentsManage = "agents.manage"
	// connectionsManage allows registering and replacing model providers and
	// secret stores.
	connectionsManage = "connections.manage"
	// sessionsRun allows creating sessions, and in one's own sessions
	// sending messages, cancelling turns, answering approval requests,
	// reading and ending them.
	sessionsRun = "sessions.run"
	// sessionsViewAll allows reading the sessions of other members.
	sessionsViewAll = "sessions.view_all"
	// sessionsControlAll allows cancelling the turns of other members,
	// answering their approval requests and ending their sessions. It
	// implies sessionsViewAll.
	sessionsControlAll = "sessions.control_all"
)

// permissions lists all permissions in their canonical order.
var permissions = []string{projectView, agentsManage, connectionsManage, sessionsRun, sessionsViewAll, sessionsControlAll}

var errAdminOnly = &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "only installation administrators can do this"}

// granted validates the requested permissions, and returns the set that they
// grant, implied permissions included, in canonical order.
func granted(asked []string) ([]string, error) {
	for _, permission := range asked {
		if !slices.Contains(permissions, permission) {
			return nil, invalid(fmt.Sprintf("unknown permission %q; the permissions are %s", permission, strings.Join(permissions, ", ")))
		}
	}
	var set []string
	for _, permission := range permissions {
		if permission == projectView || slices.Contains(asked, permission) ||
			permission == sessionsViewAll && slices.Contains(asked, sessionsControlAll) {
			set = append(set, permission)
		}
	}
	return set, nil
}

func (a *api) listRoles(w http.ResponseWriter, r *http.Request) error {
	roles, err := a.store.Roles(r.Context())
	if err != nil {
		return err
	}
	respond(w, http.StatusOK, struct {
		Roles []store.Role `json:"roles"`
	}{roles})
	return nil
}

// setRole replaces the permissions of a role. They apply to its members from
// their next request.
func (a *api) setRole(w http.ResponseWriter, r *http.Request) error {
	who := callerOf(r)
	var request struct {
		Permissions *[]string `json:"permissions"`
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if request.Permissions == nil {
		return invalid("permissions is required")
	}
	set, err := granted(*request.Permissions)
	if err != nil {
		return err
	}
	role, err := a.store.SetRole(r.Context(), r.PathValue("name"), set, who.id)
	if err != nil {
		return err
	}
	slog.InfoContext(r.Context(), "role updated", "role", role.Name, "permissions", set, "by", who.Subject)
	respond(w, http.StatusOK, role)
	return nil
}
