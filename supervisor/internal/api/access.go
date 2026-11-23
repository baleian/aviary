package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// act is an action on a session or one of its turns. Acting on one's own
// sessions and turns needs sessions.run.
type act int

const (
	// reads reads a session and its turns. Other users' sessions need
	// sessions.view_all.
	reads act = iota
	// sends sends a message. Only the user who created the session may.
	sends
	// controls cancels a turn or answers its approval requests. Turns that
	// other users sent need sessions.control_all.
	controls
	// ends ends a session. Other users' sessions need sessions.control_all.
	ends
	// retitles changes the title of a session. Other users' sessions need
	// sessions.control_all.
	retitles
)

var errNotTheirs = &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "only the session's creator can send messages to it"}

// lacks returns the error for a caller without the permission in the
// project.
func lacks(permission string) error {
	return &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "the caller does not have the " + permission + " permission in the project"}
}

// held returns the caller's permissions in the project. A caller that is not
// a member gets errNotFound, as if the project did not exist.
func (a *api) held(r *http.Request, projectID uuid.UUID) ([]string, error) {
	who := callerOf(r)
	held, err := a.store.Permissions(r.Context(), projectID, who.id, who.Groups)
	if err == nil && len(held) == 0 {
		err = errNotFound
	}
	return held, err
}

// needs are the permissions that a request needs in its project: all of
// them.
type needs []string

// heldKey is the context key of the permissions that the caller has in the
// project of the request.
type heldKey struct{}

// admit returns the request with the permissions that its caller has in the
// project, for holds. An admin has all of them. It returns an error if the
// caller lacks one that the request needs, which names the first such.
func (a *api) admit(r *http.Request, projectID uuid.UUID, needed needs) (*http.Request, error) {
	held := permissions
	if !callerOf(r).admin {
		var err error
		if held, err = a.held(r, projectID); err != nil {
			return nil, err
		}
		for _, permission := range needed {
			if !slices.Contains(held, permission) {
				return nil, lacks(permission)
			}
		}
	}
	return r.WithContext(context.WithValue(r.Context(), heldKey{}, held)), nil
}

// holds reports whether the caller has the permission in the project of the
// request. It is for the handlers behind inProject and ofProject whose
// answer depends on what else the caller may do there.
func holds(r *http.Request, permission string) bool {
	held, _ := r.Context().Value(heldKey{}).([]string)
	return slices.Contains(held, permission)
}

// mayWith returns an error unless the caller may do the act on the session,
// or on its turn that sender sent.
func (a *api) mayWith(r *http.Request, session store.Session, sender uuid.UUID, do act) error {
	who := callerOf(r)
	if who.admin {
		return nil
	}
	agent, err := a.store.Agent(r.Context(), session.AgentID)
	if err != nil {
		return err
	}
	held, err := a.held(r, agent.ProjectID)
	if err != nil {
		return err
	}
	needed := sessionsRun
	switch {
	case do == reads && session.CreatedBy != who.id:
		needed = sessionsViewAll
	case do == sends && session.CreatedBy != who.id:
		return errNotTheirs
	case do == controls && sender != who.id:
		needed = sessionsControlAll
	case (do == ends || do == retitles) && session.CreatedBy != who.id:
		needed = sessionsControlAll
	}
	if !slices.Contains(held, needed) {
		return lacks(needed)
	}
	return nil
}

// administrator passes on the requests of admins only.
func (a *api) administrator(next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		if !callerOf(r).admin {
			return errAdminOnly
		}
		return next(w, r)
	})
}

// inProject passes on the requests of callers that have the needed
// permissions in the project named by the path value pid.
func (a *api) inProject(needed needs, next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "pid")
		if err != nil {
			return err
		}
		if r, err = a.admit(r, id, needed); err != nil {
			return err
		}
		return next(w, r)
	})
}

// ofProject is like inProject for a resource whose ID is the path value
// name. project returns the ID of the resource's project.
func (a *api) ofProject(needed needs, name string, project func(*http.Request, uuid.UUID) (uuid.UUID, error), next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, name)
		if err != nil {
			return err
		}
		projectID, err := project(r, id)
		if err != nil {
			return err
		}
		if r, err = a.admit(r, projectID, needed); err != nil {
			return err
		}
		return next(w, r)
	})
}

func (a *api) ofAgent(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
	agent, err := a.store.Agent(r.Context(), id)
	return agent.ProjectID, err
}

func (a *api) ofProvider(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
	provider, err := a.store.Provider(r.Context(), id)
	return provider.ProjectID, err
}

func (a *api) ofMcpServer(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
	server, err := a.store.McpServer(r.Context(), id)
	return server.ProjectID, err
}

func (a *api) ofSecretStore(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
	secrets, err := a.store.SecretStore(r.Context(), id)
	return secrets.ProjectID, err
}

// withSession passes on the requests of callers that may do the act on the
// session named by the path value sid.
func (a *api) withSession(do act, next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "sid")
		if err != nil {
			return err
		}
		session, err := a.store.Session(r.Context(), id)
		if err != nil {
			return err
		}
		if err := a.mayWith(r, session, uuid.Nil, do); err != nil {
			return err
		}
		return next(w, r)
	})
}

// withTurn passes on the requests of callers that may do the act on the turn
// named by the path value tid.
func (a *api) withTurn(do act, next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "tid")
		if err != nil {
			return err
		}
		turn, err := a.store.Turn(r.Context(), id)
		if err != nil {
			return err
		}
		session, err := a.store.Session(r.Context(), turn.SessionID)
		if err != nil {
			return err
		}
		if err := a.mayWith(r, session, turn.CreatedBy, do); err != nil {
			return err
		}
		return next(w, r)
	})
}
