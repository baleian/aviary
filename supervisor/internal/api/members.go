package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// custom is what a member without a role is said to have for one.
const custom = "custom"

var errMemberExists = &apiError{status: http.StatusConflict, Code: "member_exists", Message: "the project has this member already"}

// memberRecord is a member of a project as the API returns it.
type memberRecord struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	// User is the member, if the member is one person. Of someone who has
	// not been seen yet it has only the username.
	User  *memberUser `json:"user,omitempty"`
	Group string      `json:"group,omitempty"`
	// Role is the role of the member, or "custom" for a member with
	// permissions of its own, which are then given.
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   uuid.UUID `json:"updated_by,omitzero"`
}

type memberUser struct {
	ID          uuid.UUID `json:"id,omitzero"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
}

func recordOf(member store.Member) memberRecord {
	record := memberRecord{
		ID: member.ID, ProjectID: member.ProjectID, Group: member.Group, Role: member.Role, Permissions: member.Permissions,
		CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt, UpdatedBy: member.UpdatedBy,
	}
	if member.Role == "" {
		record.Role = custom
	}
	if member.User != nil {
		record.User = &memberUser{ID: member.User.ID, Username: member.User.Username, DisplayName: member.User.DisplayName}
	}
	return record
}

// grant is what a request gives a member: a role, or permissions of its
// own.
type grant struct {
	Role        string    `json:"role"`
	Permissions *[]string `json:"permissions"`
}

// read returns the role of the grant, or the set of permissions that it
// comes to if it names none.
func (a *api) read(r *http.Request, given grant) (role string, set []string, err error) {
	if given.Role == "" || given.Role == custom {
		if given.Permissions == nil {
			return "", nil, invalid("a member is given a role, or the permissions of its own")
		}
		set, err = granted(*given.Permissions)
		return "", set, err
	}
	if given.Permissions != nil {
		return "", nil, invalid("a member with a role has the permissions of the role, and none of its own")
	}
	roles, err := a.store.Roles(r.Context())
	if err != nil {
		return "", nil, err
	}
	names := make([]string, len(roles))
	for i, role := range roles {
		names[i] = role.Name
	}
	if !slices.Contains(names, given.Role) {
		return "", nil, invalid(fmt.Sprintf("%q is no role: the roles are %s", given.Role, strings.Join(names, ", ")))
	}
	return given.Role, nil, nil
}

func (a *api) listMembers(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	members, err := a.store.Members(r.Context(), projectID)
	if err != nil {
		return err
	}
	records := make([]memberRecord, len(members))
	for i, member := range members {
		records[i] = recordOf(member)
	}
	respond(w, http.StatusOK, struct {
		Members []memberRecord `json:"members"`
	}{records})
	return nil
}

// addMember makes a member of a project of a user, by the username, or of a
// group of the identity provider. The user need not have been seen yet.
func (a *api) addMember(w http.ResponseWriter, r *http.Request) error {
	who := callerOf(r)
	if !who.admin {
		return errAdminOnly
	}
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	var request struct {
		User  string `json:"user"`
		Group string `json:"group"`
		grant
	}
	if err := decode(w, r, &request); err != nil {
		return err
	}
	if (request.User == "") == (request.Group == "") {
		return invalid("a member is a user, by the username, or a group")
	}
	member := store.Member{ProjectID: projectID, Group: request.Group}
	if request.User != "" {
		member.User = &store.User{Username: request.User}
	}
	if member.Role, member.Permissions, err = a.read(r, request.grant); err != nil {
		return err
	}
	added, err := a.store.AddMember(r.Context(), member, who.id)
	if err != nil {
		return err
	}
	slog.InfoContext(r.Context(), "a project was given a member", "project", projectID, "member", added.ID,
		"user", request.User, "group", request.Group, "role", added.Role, "permissions", added.Permissions, "by", who.Subject)
	respond(w, http.StatusCreated, recordOf(added))
	return nil
}

// setMember gives a member another role, or other permissions of its own.
func (a *api) setMember(w http.ResponseWriter, r *http.Request) error {
	who := callerOf(r)
	if !who.admin {
		return errAdminOnly
	}
	id, err := pathID(r, "mid")
	if err != nil {
		return err
	}
	var request grant
	if err := decode(w, r, &request); err != nil {
		return err
	}
	role, set, err := a.read(r, request)
	if err != nil {
		return err
	}
	member, err := a.store.SetMember(r.Context(), id, role, set, who.id)
	if err != nil {
		return err
	}
	slog.InfoContext(r.Context(), "a member was given what it may do", "project", member.ProjectID, "member", id,
		"role", role, "permissions", set, "by", who.Subject)
	respond(w, http.StatusOK, recordOf(member))
	return nil
}

func (a *api) removeMember(w http.ResponseWriter, r *http.Request) error {
	who := callerOf(r)
	if !who.admin {
		return errAdminOnly
	}
	id, err := pathID(r, "mid")
	if err != nil {
		return err
	}
	if err := a.store.RemoveMember(r.Context(), id); err != nil {
		return err
	}
	slog.InfoContext(r.Context(), "a member was removed", "member", id, "by", who.Subject)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
