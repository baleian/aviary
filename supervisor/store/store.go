// Package store defines the records of the Supervisor and the Store interface
// that keeps them in a database. A Plugin provides the Store for one kind of
// database.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrSessionBusy  = errors.New("session has an unfinished turn")
	ErrSessionEnded = errors.New("session has ended")
	ErrQueueFull    = errors.New("turn queue is full")
	ErrMemberExists = errors.New("member already exists")
	ErrSlugTaken    = errors.New("slug is taken")
	ErrTurnEnded    = errors.New("turn has ended")
	ErrAgentDeleted = errors.New("agent is deleted")
)

// Turn states. A queued turn waits to run, a dispatching turn waits for a
// Supervisor to follow it, and a running turn is followed. A turn ends as
// completed, failed or cancelled, or as expired if it was queued too long.
const (
	TurnQueued      = "queued"
	TurnDispatching = "dispatching"
	TurnRunning     = "running"
	TurnCompleted   = "completed"
	TurnFailed      = "failed"
	TurnCancelled   = "cancelled"
	TurnExpired     = "expired"
)

type Project struct {
	ID uuid.UUID `json:"id"`
	// Slug is unique among the projects.
	Slug string `json:"slug"`
	// Name is a display name. It is empty if none was given.
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// User is a user as authenticated by the identity provider of the
// installation.
type User struct {
	ID uuid.UUID `json:"id"`
	// Subject is the provider's stable identifier for the user.
	Subject string `json:"subject"`
	// Username is a human-readable name, unique among the users. It is empty
	// if the provider gave none.
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Agent struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	// Slug is unique among the agents of the project.
	Slug string `json:"slug"`
	// Name is a display name. It is empty if none was given.
	Name string `json:"name"`
	// Description says what the agent is for, for display. It is empty if
	// none was given.
	Description string    `json:"description"`
	Harness     string    `json:"harness"`
	CreatedAt   time.Time `json:"created_at"`
	// UpdatedAt is when the agent was last replaced.
	UpdatedAt time.Time `json:"updated_at"`
	// DeletedAt is set if the agent is deleted: it is no longer listed and
	// gets no new sessions, and its sessions have ended.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	// Instructions, if not empty, are given to the agent before every turn.
	Instructions string `json:"instructions,omitempty"`
	// Approval is "ask" if the harness asks a person for approval before it
	// acts. If empty, the harness does not ask.
	Approval string `json:"approval,omitempty"`
	Model    Model  `json:"model"`
}

// Model names a model of a provider.
type Model struct {
	// Provider is the ID of a provider in the agent's project.
	Provider uuid.UUID `json:"provider"`
	// Name is the provider's name for the model.
	Name string `json:"name"`
}

// Role is a named set of permissions that project members can be given. The
// store does not interpret the permissions.
type Role struct {
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	UpdatedAt   time.Time `json:"updated_at"`
	// UpdatedBy is the user who last changed the permissions. It is uuid.Nil
	// if they were never changed or the request was not authenticated.
	UpdatedBy uuid.UUID `json:"updated_by,omitzero"`
}

// Member grants a user or a group permissions in a project.
type Member struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// User is set if the member is a single user. For an awaited user, one
	// that the store does not know yet, only its Username is set.
	User *User
	// Group is the name of an identity provider group, if the member is a
	// group.
	Group string
	// Role is the name of the member's role. If empty, the member has
	// Permissions instead.
	Role        string
	Permissions []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// UpdatedBy is the user who last set the role or permissions, or
	// uuid.Nil if the request was not authenticated.
	UpdatedBy uuid.UUID
}

type Session struct {
	ID      uuid.UUID `json:"id"`
	AgentID uuid.UUID `json:"agent_id"`
	// Title is a display title. It is empty if none was given.
	Title string `json:"title"`
	// CreatedBy is the user who created and owns the session, or uuid.Nil
	// if the request was not authenticated.
	CreatedBy uuid.UUID  `json:"created_by,omitzero"`
	CreatedAt time.Time  `json:"created_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// EndReason is EndExpired or EndAgentDeleted if the session ended for
	// that reason. It is empty if a caller ended the session, or the session
	// has not ended.
	EndReason string `json:"end_reason,omitempty"`
	// Sandbox is the name of the session's sandbox. It is empty if the
	// session has none: it has run no turn yet, or its sandbox was deleted.
	Sandbox string `json:"-"`
	// SandboxState is SandboxAwake, SandboxAsleep or SandboxLost. It is empty
	// if the session has no sandbox.
	SandboxState string `json:"sandbox,omitempty"`
}

// Sandbox states. An awake sandbox has a pod, or is getting one. The pod of
// an asleep sandbox was deleted, and its volume is kept. A lost sandbox is
// gone with its volume, although its session has not ended: the session's
// next turn gets a new one.
const (
	SandboxAwake  = "awake"
	SandboxAsleep = "asleep"
	SandboxLost   = "lost"
)

// End reasons of the sessions that no caller ended.
const (
	// EndExpired is the end reason of a session that was unused for longer
	// than the installation keeps sandboxes: its sandbox was deleted with
	// the agent's files.
	EndExpired = "expired"
	// EndAgentDeleted is the end reason of a session whose agent was
	// deleted.
	EndAgentDeleted = "agent_deleted"
)

// Provider is a model provider of a project: a model endpoint and its API
// key. A provider has a stored key, a key in a secret store, or no key.
type Provider struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	// URL is the base URL of the model endpoint.
	URL string `json:"url"`
	// APIKey is the stored API key, sealed for ProviderKeyPlace.
	APIKey []byte `json:"-"`
	// APIKeyAt locates the API key if it is kept in a secret store.
	APIKeyAt  *SecretReference `json:"-"`
	CreatedAt time.Time        `json:"created_at"`
	// UpdatedAt is when the provider was last replaced.
	UpdatedAt time.Time `json:"updated_at"`
}

// SecretReference locates a value in a secret store of the project: the path
// of a secret in the store and the key of the value within the secret.
type SecretReference struct {
	Store uuid.UUID `json:"store"`
	Path  string    `json:"path"`
	Key   string    `json:"key"`
}

// SecretStore is an external secret store in which a project keeps secrets
// outside Aviary.
type SecretStore struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	// Kind selects the secret store plugin that reads the store.
	Kind string
	// Settings are the settings of the store that its kind defines, without
	// secrets.
	Settings json.RawMessage
	// Credential authenticates the Supervisor to the store. It is sealed for
	// SecretStoreCredentialPlace.
	Credential []byte
	CreatedAt  time.Time
	// UpdatedAt is when the store was last replaced.
	UpdatedAt time.Time
}

// SecretStoreCredentialPlace returns the place that a secret store's
// credential is sealed for.
func SecretStoreCredentialPlace(id uuid.UUID) string {
	return "secret_stores/" + id.String() + "/credential"
}

// ProviderKeyPlace returns the place that a provider's API key is sealed for.
// A secret sealed for one place cannot be opened for another.
func ProviderKeyPlace(id uuid.UUID) string {
	return "providers/" + id.String() + "/api_key"
}

// Turn is a message to a session and the work that the agent does in reply.
type Turn struct {
	ID        uuid.UUID `json:"id"`
	SessionID uuid.UUID `json:"session_id"`
	Seq       int       `json:"seq"`
	State     string    `json:"state"`
	Text      string    `json:"text"`
	// CreatedBy is the user who sent the message, or uuid.Nil if the request
	// was not authenticated.
	CreatedBy uuid.UUID `json:"created_by,omitzero"`
	CreatedAt time.Time `json:"created_at"`
	// StartedAt is when the harness received the message.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// StopReason is why the harness ended the turn, as the runner reported it.
	StopReason        *string    `json:"stop_reason,omitempty"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	Usage             *Usage     `json:"usage,omitempty"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	Failure           *Failure   `json:"failure,omitempty"`
}

// Failure describes why a turn failed.
type Failure struct {
	// Reason is one of the Failure constants.
	Reason string `json:"reason"`
	// Message is a human-readable description of the failure.
	Message string `json:"message,omitempty"`
}

// Failure reasons.
const (
	// FailureHarness means that the harness could not be started or returned
	// an error, as it does when the model provider refuses a request.
	FailureHarness = "harness_error"
	// FailureStartup means that the harness did not receive the message in
	// time, for example because the session's sandbox could not be created.
	FailureStartup = "startup_timed_out"
	// FailureResume means that the harness could not resume its session
	// because its saved conversation is missing or unreadable. The session
	// keeps its harness session, and its next turn tries again.
	FailureResume = "resume_failed"
	// FailureTimedOut means that the turn ran longer than a turn may.
	FailureTimedOut = "timed_out"
	// FailureSandboxLost means that the session's sandbox was lost, with the
	// agent's files in it.
	FailureSandboxLost = "sandbox_lost"
	// FailureSessionExpired means that the session was unused for longer than
	// the installation keeps sandboxes: its sandbox was deleted with the
	// agent's files, and the session has ended.
	FailureSessionExpired = "session_expired"
	// FailureRunner means that the runner failed in some other way.
	FailureRunner = "runner_error"
)

// Usage is the token usage of a turn as reported by its harness. Harnesses
// count differently: some report the sum over the turn's model requests,
// others only the last request.
type Usage struct {
	TotalTokens       int64  `json:"total_tokens"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	ThoughtTokens     *int64 `json:"thought_tokens,omitempty"`
	CachedReadTokens  *int64 `json:"cached_read_tokens,omitempty"`
	CachedWriteTokens *int64 `json:"cached_write_tokens,omitempty"`
}

// DispatchedTurn is a dispatched turn that has not ended.
type DispatchedTurn struct {
	ID uuid.UUID
	// Cancelling is how long ago cancellation was requested, or zero if it
	// was not.
	Cancelling time.Duration
}

// UnfinishedTurn is a turn that has not ended, as the state metrics count it.
type UnfinishedTurn struct {
	// Harness is that of the session's agent.
	Harness string
	// State is TurnQueued, TurnDispatching or TurnRunning.
	State string
	// Started reports whether the harness has received the message.
	Started bool
	// Age is how long ago the turn left the queue or, for a queued turn, how
	// long ago its message was sent.
	Age time.Duration
	// Asking is the number of the turn's approval requests that wait for a
	// person's answer.
	Asking int
}

// FollowedTurn is a turn with what the Supervisor that follows it needs to
// run it.
type FollowedTurn struct {
	Turn
	AgentID   uuid.UUID
	ProjectID uuid.UUID
	// Harness, Instructions and Approval are those of the session's agent.
	Harness      string
	Instructions string
	Approval     string
	ProviderURL  string
	Model        string
	// Sandbox is empty if the session has no sandbox.
	Sandbox string
	// Dispatched is how long ago the turn was dispatched.
	Dispatched time.Duration
}

// Dispatch is a turn that DispatchTurns dispatched.
type Dispatch struct {
	TurnID  uuid.UUID
	Harness string
	// Waited is how long the turn was queued.
	Waited time.Duration
}

// Origin identifies the source of a request from a sandbox: the turn that
// runs in it, and the session, agent and project of the turn.
type Origin struct {
	TurnID    uuid.UUID
	SessionID uuid.UUID
	AgentID   uuid.UUID
	ProjectID uuid.UUID
	// UserID and Subject identify the user who sent the turn's message. They
	// are zero if the request was not authenticated.
	UserID  uuid.UUID
	Subject string
	// Provider is the current provider of the agent's model. Its name and
	// times are not set.
	Provider Provider
	// SecretStore is the current secret store of the provider's API key, if
	// the key is kept in one.
	SecretStore *SecretStore
}

// Approval is a request of a turn's harness for a person's approval.
type Approval struct {
	// Request is the request as the runner reported it.
	Request json.RawMessage
	// Answered reports whether a person has chosen an option, and Resolved
	// whether the harness has received its answer.
	Answered, Resolved bool
	// AnsweredBy is the user who chose the option. It is uuid.Nil if there is
	// no answer or the request was not authenticated.
	AnsweredBy uuid.UUID
}

// Answer is the option that a person chose for an approval request, not yet
// delivered to the harness.
type Answer struct {
	TurnID       uuid.UUID
	ID, OptionID string
}

// OptionKind returns the kind of the request's option with the given ID, or
// "" if the request has no such option.
func (a Approval) OptionKind(id string) string {
	var request struct {
		Options []struct{ ID, Kind string } `json:"options"`
	}
	if json.Unmarshal(a.Request, &request) != nil {
		return ""
	}
	for _, option := range request.Options {
		if option.ID == id {
			return option.Kind
		}
	}
	return ""
}

// Store keeps the records in a database that all Supervisors of an
// installation share. Its guarantees hold when Supervisors call it
// concurrently.
//
// Methods that read one record by ID return ErrNotFound if there is none.
// The by argument of a method that changes records is the user who makes the
// change, or uuid.Nil if the request was not authenticated. JSON is returned
// as an equal value, not necessarily as the same text. A method whose context
// is done fails without effect.
type Store interface {
	// Close closes the connections to the database.
	Close()

	// CreateProject creates the project. It returns ErrSlugTaken if another
	// project has its slug.
	CreateProject(ctx context.Context, project Project) (Project, error)

	Project(ctx context.Context, id uuid.UUID) (Project, error)

	// Projects returns all projects in creation order.
	Projects(ctx context.Context) ([]Project, error)

	// ProjectsOf returns the projects in which the user or one of the groups
	// is a member, in creation order.
	ProjectsOf(ctx context.Context, userID uuid.UUID, groups []string) ([]Project, error)

	// UserOf returns the user with the subject, and creates it if there is
	// none; concurrent calls for a new subject create one user. It sets the
	// display name, and the username unless username is empty. Usernames are
	// unique: a username given to one user is taken from any other. A user
	// given a username under which projects await a member becomes that
	// member.
	UserOf(ctx context.Context, subject, username, displayName string) (User, error)

	// AddMember adds the user, given by username, or the group of member to
	// its project, with its Role or, if that is empty, its Permissions. A
	// username that no user has yet is awaited: the user that later gets it
	// becomes the member, in place of any membership that user has in the
	// project by then. It returns ErrNotFound if the project does not exist,
	// and ErrMemberExists if the user, username or group is a member already.
	AddMember(ctx context.Context, member Member, by uuid.UUID) (Member, error)

	// SetMember replaces the member's role, or its permissions if role is
	// empty.
	SetMember(ctx context.Context, id uuid.UUID, role string, permissions []string, by uuid.UUID) (Member, error)

	RemoveMember(ctx context.Context, id uuid.UUID) error

	// Members returns the members of the project in the order in which they
	// were added.
	Members(ctx context.Context, projectID uuid.UUID) ([]Member, error)

	// Permissions returns the permissions that the project grants the user and
	// the groups through their memberships, sorted and without duplicates. A
	// member with a role has the role's current permissions.
	Permissions(ctx context.Context, projectID, userID uuid.UUID, groups []string) ([]string, error)

	// Roles returns the roles sorted by name. The roles are predefined; only
	// their permissions change.
	Roles(ctx context.Context) ([]Role, error)

	// SetRole replaces the permissions of the named role, keeping their
	// order. It returns ErrNotFound if there is no such role.
	SetRole(ctx context.Context, name string, permissions []string, by uuid.UUID) (Role, error)

	// CreateAgent creates the agent in the project. It returns ErrNotFound if
	// the project does not exist, ErrSlugTaken if another agent of the project
	// has its slug and is not deleted, and an error if the provider of its
	// model does not exist.
	CreateAgent(ctx context.Context, projectID uuid.UUID, agent Agent) (Agent, error)

	// Agent returns the agent, also if it is deleted.
	Agent(ctx context.Context, id uuid.UUID) (Agent, error)

	// ReplaceAgent replaces the name, description, instructions, approval and
	// model of the agent with the same ID. It returns ErrNotFound if there is
	// no such agent, ErrAgentDeleted if it is deleted, and an error if the
	// provider of its model does not exist.
	ReplaceAgent(ctx context.Context, agent Agent) (Agent, error)

	// DeleteAgent marks the agent as deleted and ends its sessions that have
	// not ended, with the reason EndAgentDeleted. It returns the agent and
	// the sessions that it ended, including their sandboxes. An agent that is
	// already deleted is returned unchanged, without sessions. DeleteAgent
	// returns ErrNotFound if there is no such agent, and ErrSessionBusy if a
	// turn of one of its sessions has not ended.
	DeleteAgent(ctx context.Context, id uuid.UUID) (Agent, []Session, error)

	// Agents returns the agents of the project that are not deleted, in
	// creation order.
	Agents(ctx context.Context, projectID uuid.UUID) ([]Agent, error)

	// CreateSession creates a session of the agent with the title, which may
	// be empty. It returns ErrNotFound if the agent does not exist, and
	// ErrAgentDeleted if it is deleted.
	CreateSession(ctx context.Context, agentID, by uuid.UUID, title string) (Session, error)

	Session(ctx context.Context, id uuid.UUID) (Session, error)

	// SetSessionTitle replaces the session's title, which may be empty, and
	// returns the session. It returns ErrNotFound if there is no such session.
	SetSessionTitle(ctx context.Context, id uuid.UUID, title string) (Session, error)

	// SetSandbox changes the session's sandbox from from to to, where ""
	// means none. A sandbox that is set is awake. It is a compare-and-swap:
	// it reports false and changes nothing if the session's sandbox is not
	// from, as when another Supervisor has changed it meanwhile. It returns
	// ErrNotFound if there is no such session.
	SetSandbox(ctx context.Context, sessionID uuid.UUID, from, to string) (bool, error)

	// SetSandboxState records that the session's sandbox is awake, asleep or
	// lost. It changes nothing if the session's sandbox is not the named one,
	// as when the sandbox was deleted meanwhile.
	SetSandboxState(ctx context.Context, sessionID uuid.UUID, sandbox, state string) error

	// SessionsWithSandbox returns the sessions that have a sandbox, in no
	// particular order. Sessions that have ended are among them.
	SessionsWithSandbox(ctx context.Context) ([]Session, error)

	// SandboxCounts returns the number of sandboxes in each state, keyed by
	// state. It counts the sandboxes of the sessions that have not ended. A
	// state without sandboxes may be absent from the map.
	SandboxCounts(ctx context.Context) (map[string]int, error)

	// EndSession ends the session, which then accepts no messages, and
	// returns it, including its sandbox. reason is EndExpired, or empty if a
	// caller ends the session. A session that has already ended is returned
	// unchanged. It returns ErrNotFound if there is no such session, and
	// ErrSessionBusy if one of its turns has not ended.
	EndSession(ctx context.Context, id uuid.UUID, reason string) (Session, error)

	// Sessions returns up to limit sessions of the agent with IDs below
	// before, newest first: those created by by, or all if by is uuid.Nil.
	// Session IDs increase with creation time, so the last ID of a page is
	// the cursor for the next.
	Sessions(ctx context.Context, agentID, by, before uuid.UUID, limit int) ([]Session, error)

	// CreateProvider creates the provider with the ID that it is given, since
	// its API key is sealed for that ID. It returns ErrNotFound if the project
	// does not exist, and an error if the key's secret store does not.
	CreateProvider(ctx context.Context, provider Provider) (Provider, error)

	Provider(ctx context.Context, id uuid.UUID) (Provider, error)

	// Providers returns the providers of the project in creation order.
	Providers(ctx context.Context, projectID uuid.UUID) ([]Provider, error)

	// ReplaceProvider replaces the name, URL and API key of the provider with
	// the same ID; a provider given no key has none afterwards. It returns
	// ErrNotFound if there is no such provider.
	ReplaceProvider(ctx context.Context, provider Provider) (Provider, error)

	// CreateSecretStore creates the secret store with the ID that it is
	// given, since its credential is sealed for that ID. It returns
	// ErrNotFound if the project does not exist.
	CreateSecretStore(ctx context.Context, secrets SecretStore) (SecretStore, error)

	SecretStore(ctx context.Context, id uuid.UUID) (SecretStore, error)

	// SecretStores returns the secret stores of the project in creation order.
	SecretStores(ctx context.Context, projectID uuid.UUID) ([]SecretStore, error)

	// ReplaceSecretStore replaces the name, kind, settings and credential of
	// the secret store with the same ID. It returns ErrNotFound if there is no
	// such store.
	ReplaceSecretStore(ctx context.Context, secrets SecretStore) (SecretStore, error)

	// CreateTurn queues text as the session's next turn. It also returns how
	// many turns then wait to run, including the new one; queued turns that
	// have room among maxRunning do not wait. It returns ErrNotFound if the
	// session does not exist, ErrSessionEnded if it has ended, ErrSessionBusy
	// if it has an unfinished turn, and ErrQueueFull if more than maxWaiting
	// turns would wait. A maxWaiting of 0 means no limit.
	CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, maxWaiting, maxRunning int) (Turn, int, error)

	// AnnounceQueue calls announce with the queued turns in the order in which
	// they will run. Calls are serialized across Supervisors, so that queue
	// positions are never announced out of order.
	AnnounceQueue(ctx context.Context, announce func(queued []uuid.UUID) error) error

	// CancellingTurns returns the unfinished turns whose cancellation was
	// requested.
	CancellingTurns(ctx context.Context) ([]uuid.UUID, error)

	Turn(ctx context.Context, id uuid.UUID) (Turn, error)

	// Turns returns up to limit turns of the session with sequence numbers
	// below before, newest first.
	Turns(ctx context.Context, sessionID uuid.UUID, before, limit int) ([]Turn, error)

	// LastTurns returns the newest turn of each of the sessions, keyed by
	// session ID. A session without turns is absent from the map.
	LastTurns(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID]Turn, error)

	// TurnItems returns what the agent said and did in each of the turns, in
	// order, keyed by turn ID. A turn has items only once it has ended, and
	// turns without items are absent from the map.
	TurnItems(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]json.RawMessage, error)

	// DispatchTurns dispatches queued turns, oldest first, while fewer than
	// limit turns are dispatched or running. It returns the turns that it
	// dispatched.
	DispatchTurns(ctx context.Context, limit int) ([]Dispatch, error)

	// CancelTurn cancels the turn. A queued turn ends at once; a dispatched
	// turn is marked, and ends when its harness has stopped. CancelTurn returns
	// the updated turn and whether it had not ended yet.
	CancelTurn(ctx context.Context, id uuid.UUID) (Turn, bool, error)

	// ExpireTurns ends the turns that have been queued for longer than wait as
	// expired, and returns them.
	ExpireTurns(ctx context.Context, wait time.Duration) ([]Turn, error)

	DispatchedTurns(ctx context.Context) ([]DispatchedTurn, error)

	// UnfinishedTurns returns the turns that have not ended, in no particular
	// order.
	UnfinishedTurns(ctx context.Context) ([]UnfinishedTurn, error)

	// FollowTurn marks a dispatched turn as running and returns it to the
	// Supervisor that follows it, which runs the turn and records its outcome.
	// Which Supervisor follows a turn is settled in the event log. FollowTurn
	// returns ErrNotFound if there is no such turn or it is still queued, and
	// ErrTurnEnded if it has ended.
	FollowTurn(ctx context.Context, turnID uuid.UUID) (FollowedTurn, error)

	// SessionTurn returns the origin of the turn that runs in the session, or
	// ErrNotFound if no turn runs in it.
	SessionTurn(ctx context.Context, sessionID uuid.UUID) (Origin, error)

	// StartTurn records that the turn's harness has received the message, and
	// returns the time since the turn's dispatch. It reports false, and
	// changes nothing, if the turn had already started.
	StartTurn(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error)

	// TurnHarness returns the harness of the turn's agent, or ErrNotFound if
	// there is no such turn.
	TurnHarness(ctx context.Context, turnID uuid.UUID) (string, error)

	// SetFinished records why the harness ended the turn and, if usage is not
	// nil, the turn's token usage.
	SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *Usage) error

	// EndTurn ends the turn: as completed if succeeded is true and SetFinished
	// recorded a stop reason, and otherwise as failed with failure. A turn
	// whose cancellation was requested ends as cancelled, unless its harness
	// had ended it by itself. The session then accepts its next message.
	// EndTurn reports false if the turn had already ended or does not exist.
	//
	// EndTurn calls items only for a turn that it ends, and stores the result
	// as what the agent said and did in the turn; if items fails, the turn
	// does not end. A database that cannot store U+0000 stores U+FFFD in its
	// place, here and in approval requests.
	EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure Failure,
		items func(context.Context) ([]json.RawMessage, error)) (Turn, bool, error)

	// AddApproval stores an approval request of the turn's harness under the
	// ID that the runner gave it. Adding a request that exists changes
	// nothing. It fails if the turn does not exist.
	AddApproval(ctx context.Context, turnID uuid.UUID, id string, request json.RawMessage) error

	// ResolveApproval records that the harness has received its answer to the
	// request, and the outcome. It returns the request and how long it waited,
	// and reports false if the request was already resolved or does not exist.
	ResolveApproval(ctx context.Context, turnID uuid.UUID, id, outcome string) (Approval, time.Duration, bool, error)

	// Approval returns the turn's approval request with the given ID, or
	// ErrNotFound if there is none.
	Approval(ctx context.Context, turnID uuid.UUID, id string) (Approval, error)

	// AnswerApproval records the option that a person chose for the request.
	// The first answer stands: it reports false if the request has an answer
	// already, is resolved, or its turn has ended.
	AnswerApproval(ctx context.Context, turnID uuid.UUID, id, optionID string, by uuid.UUID) (bool, error)

	// WaitingApprovals returns the turn's requests that wait for an answer,
	// oldest first.
	WaitingApprovals(ctx context.Context, turnID uuid.UUID) ([]json.RawMessage, error)

	// Answers returns the answers that have not yet been delivered to the
	// harnesses of unfinished turns: those of turnID, or of all turns if
	// turnID is nil.
	Answers(ctx context.Context, turnID *uuid.UUID) ([]Answer, error)
}

// Plugin provides the Store for one kind of database, which it recognizes by
// the scheme of the database URL.
type Plugin interface {
	// Schemes returns the URL schemes that the plugin handles.
	Schemes() []string
	// Open connects to the database at address. It fails if the schema does
	// not suit this build, as when it has not been migrated.
	Open(ctx context.Context, address string) (Store, error)
	// Migrate brings the schema of the database at address up to date.
	Migrate(ctx context.Context, address string) error
	// MigrateTo migrates the schema to version, forward or back.
	MigrateTo(ctx context.Context, address string, version int64) error
}

// plugin returns the plugin for the scheme of address.
func plugin(address string, plugins []Plugin) (Plugin, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		// A url.Error quotes the URL, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	var known []string
	for _, plugin := range plugins {
		if slices.Contains(plugin.Schemes(), parsed.Scheme) {
			return plugin, nil
		}
		known = append(known, plugin.Schemes()...)
	}
	return nil, fmt.Errorf("unsupported database URL scheme %q (supported: %s)",
		parsed.Scheme, strings.Join(known, ", "))
}

// Open connects to the database at address with the plugin for its URL
// scheme.
func Open(ctx context.Context, address string, plugins ...Plugin) (Store, error) {
	plugin, err := plugin(address, plugins)
	if err != nil {
		return nil, err
	}
	return plugin.Open(ctx, address)
}

// Migrate brings the schema of the database at address up to date.
func Migrate(ctx context.Context, address string, plugins ...Plugin) error {
	plugin, err := plugin(address, plugins)
	if err != nil {
		return err
	}
	return plugin.Migrate(ctx, address)
}

// MigrateTo migrates the schema of the database at address to version,
// forward or back.
func MigrateTo(ctx context.Context, address string, version int64, plugins ...Plugin) error {
	plugin, err := plugin(address, plugins)
	if err != nil {
		return err
	}
	return plugin.MigrateTo(ctx, address, version)
}
