package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"aviary/supervisor/store"
)

// The permissions: what a member of a project may do there. A role is a set
// of them.
const (
	// projectView is to see the project and what it is made of: its agents,
	// its model providers and its secret stores, without their secrets.
	// Every member may.
	projectView = "project.view"
	// agentsManage is to create the agents of the project.
	agentsManage = "agents.manage"
	// connectionsManage is to register and to replace the model providers
	// and the secret stores of the project.
	connectionsManage = "connections.manage"
	// sessionsRun is to create sessions, and in one's own to send messages,
	// to stop the turns, to answer their requests for approval, to read
	// what was said and to end it.
	sessionsRun = "sessions.run"
	// sessionsViewAll is to read the sessions of the other members.
	sessionsViewAll = "sessions.view_all"
	// sessionsControlAll is to stop the turns of the other members, to
	// answer their requests for approval and to end their sessions. Who may
	// do that may read them.
	sessionsControlAll = "sessions.control_all"
)

// permissions are the permissions in the order in which a set of them is
// written.
var permissions = []string{projectView, agentsManage, connectionsManage, sessionsRun, sessionsViewAll, sessionsControlAll}

var errAdminOnly = &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "only an administrator of the installation may do this"}

// granted returns the set of permissions that those that are asked for come
// to: every set has the permission to see the project, and one with the
// permission to control the sessions of others has the one to read them. It
// tells of a name that is no permission.
func granted(asked []string) ([]string, error) {
	for _, permission := range asked {
		if !slices.Contains(permissions, permission) {
			return nil, invalid(fmt.Sprintf("%q is no permission: the permissions are %s", permission, strings.Join(permissions, ", ")))
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

// setRole gives a role its permissions. The members that have the role have
// them from their next request on.
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
	slog.InfoContext(r.Context(), "a role was given its permissions", "role", role.Name, "permissions", set, "by", who.Subject)
	respond(w, http.StatusOK, role)
	return nil
}
