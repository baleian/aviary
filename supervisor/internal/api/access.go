package api

import (
	"net/http"
	"slices"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// act is what a caller does with a session, or with a turn of one.
type act int

const (
	// reads is to read what was said: one's own sessions with the
	// permission to run sessions, and those of others with the one to view
	// them all.
	reads act = iota
	// sends is to send a message, which only the one whose session it is
	// does.
	sends
	// controls is to stop a turn or to answer its request for approval: of
	// a turn that one sent oneself with the permission to run sessions, and
	// of another's with the one to control them all.
	controls
	// ends is to end a session: one's own with the permission to run
	// sessions, and another's with the one to control them all.
	ends
)

var errNotTheirs = &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "only the one whose session it is sends messages to it"}

// lacks is the error for a member of a project who may not do there what
// the permission is for.
func lacks(permission string) error {
	return &apiError{status: http.StatusForbidden, Code: "forbidden", Message: "the caller lacks the permission " + permission + " in the project"}
}

// held returns what the caller of the request may do in the project. A
// caller who is no member of the project is answered as if there were no
// such project: errNotFound.
func (a *api) held(r *http.Request, projectID uuid.UUID) ([]string, error) {
	who := callerOf(r)
	held, err := a.store.Permissions(r.Context(), projectID, who.id, who.Groups)
	if err == nil && len(held) == 0 {
		err = errNotFound
	}
	return held, err
}

// may tells whether the caller of the request may do in the project what
// the permission is for. An administrator may do everything everywhere.
func (a *api) may(r *http.Request, projectID uuid.UUID, permission string) error {
	if callerOf(r).admin {
		return nil
	}
	held, err := a.held(r, projectID)
	if err == nil && !slices.Contains(held, permission) {
		err = lacks(permission)
	}
	return err
}

// mayWith tells whether the caller of the request may do the act with the
// session, or with the turn of it whose message the sender sent.
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
	case do == ends && session.CreatedBy != who.id:
		needed = sessionsControlAll
	}
	if !slices.Contains(held, needed) {
		return lacks(needed)
	}
	return nil
}

// administrator serves the requests of the administrators of the
// installation.
func (a *api) administrator(next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		if !callerOf(r).admin {
			return errAdminOnly
		}
		return next(w, r)
	})
}

// inProject serves the requests of the callers that have the permission in
// the project of the request.
func (a *api) inProject(permission string, next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "pid")
		if err != nil {
			return err
		}
		if err := a.may(r, id, permission); err != nil {
			return err
		}
		return next(w, r)
	})
}

// ofProject serves the requests of the callers that have the permission in
// the project of what the request names: the function returns that project,
// of what has the ID in the path under the name.
func (a *api) ofProject(permission, name string, project func(*http.Request, uuid.UUID) (uuid.UUID, error), next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, name)
		if err != nil {
			return err
		}
		projectID, err := project(r, id)
		if err != nil {
			return err
		}
		if err := a.may(r, projectID, permission); err != nil {
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

func (a *api) ofSecretStore(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
	secrets, err := a.store.SecretStore(r.Context(), id)
	return secrets.ProjectID, err
}

// withSession serves the requests of the callers that may do the act with
// the session of the request.
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

// withTurn serves the requests of the callers that may do the act with the
// turn of the request.
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
