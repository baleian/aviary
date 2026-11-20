// Package store is the records of the Supervisor: the projects, their model
// providers, secret stores and agents, the sessions of the agents and the
// turns of the sessions. The records are in a database, which a plugin
// connects the Supervisor to.
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
	ErrQueueFull    = errors.New("too many turns wait to run")
	// ErrMemberExists is the error for a member that a project has already.
	ErrMemberExists = errors.New("the project has this member already")
	ErrTurnEnded    = errors.New("turn has ended")
)

// The states of a turn. A queued turn waits to run, a dispatching one waits
// for a Supervisor to follow it, and a running one is followed. A turn ends
// as completed, failed or cancelled, or as expired if it waited too long.
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
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// User is someone whom the identity provider of the installation told of
// when they sent a request.
type User struct {
	ID uuid.UUID `json:"id"`
	// Subject is what the provider knows the user by. It never changes.
	Subject string `json:"subject"`
	// Username is a name of the user that a person can read and that no
	// other user has at the provider. It is empty if the provider gave none.
	Username string `json:"username,omitempty"`
	// DisplayName is what the user is shown as.
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Agent struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Harness   string    `json:"harness"`
	CreatedAt time.Time `json:"created_at"`
	// Instructions are what the agent is told before every turn, if anything.
	Instructions string `json:"instructions,omitempty"`
	// Approval is "ask" if a person answers what the harness asks before it
	// acts. Nothing is asked of an agent without it.
	Approval string `json:"approval,omitempty"`
	// Model is the model that the agent's turns use, if the agent names one.
	Model *Model `json:"model,omitempty"`
}

// Model is a model of a provider.
type Model struct {
	// Provider is the ID of a provider of the agent's project.
	Provider uuid.UUID `json:"provider"`
	// Name is the name of the model as the provider knows it.
	Name string `json:"name"`
}

// Role is what a member of a project may do: a set of permissions under a
// name. The store keeps the names of the permissions and does not know what
// they mean.
type Role struct {
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	UpdatedAt   time.Time `json:"updated_at"`
	// UpdatedBy is the user who changed the permissions last. It is
	// uuid.Nil if nobody has, or nobody who was asked who they are.
	UpdatedBy uuid.UUID `json:"updated_by,omitzero"`
}

// Member is who may do something in a project, and what.
type Member struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// User is the member, if the member is one person: the user, or only
	// the username of someone who has not been seen yet.
	User *User
	// Group is the name of the group of the identity provider whose members
	// are the member, if the member is not one person.
	Group string
	// Role is the name of the role whose permissions the member has. It is
	// empty for a member with permissions of its own.
	Role string
	// Permissions are those of a member without a role.
	Permissions []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// UpdatedBy is the user who gave the member what it may do. It is
	// uuid.Nil if nobody did who was asked who they are.
	UpdatedBy uuid.UUID
}

type Session struct {
	ID      uuid.UUID `json:"id"`
	AgentID uuid.UUID `json:"agent_id"`
	// CreatedBy is the user who created the session, whose session it is.
	// It is uuid.Nil for a session of an installation that asks nobody who
	// they are.
	CreatedBy uuid.UUID `json:"created_by,omitzero"`
	CreatedAt time.Time `json:"created_at"`
	// EndedAt is when the session was ended, if it was. A session that has
	// ended takes no message.
	EndedAt *time.Time `json:"ended_at,omitempty"`
	// Sandbox is the sandbox that the session runs in, as the sandbox
	// service names it. It is empty for a session that has none: one that
	// has run no turn yet, or whose sandbox is gone.
	Sandbox string `json:"-"`
}

// Provider is a model provider of a project: a model endpoint and the key
// to it.
type Provider struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	// URL is the base URL of the model endpoint.
	URL string `json:"url"`
	// APIKey is the API key of the endpoint as it is stored: sealed for the
	// place that ProviderKeyPlace names. An endpoint that takes no key has
	// none.
	APIKey []byte `json:"-"`
	// APIKeyAt is where the API key is, if it is kept in a secret store of
	// the project and not stored. A provider has a key that is stored, a
	// key in a secret store, or no key.
	APIKeyAt  *SecretReference `json:"-"`
	CreatedAt time.Time        `json:"created_at"`
	// UpdatedAt is when the URL and the key were last given.
	UpdatedAt time.Time `json:"updated_at"`
}

// SecretReference is where a secret is in a secret store of a project: the
// store, the path of the secret in it, and the key of the value among those
// that the secret holds.
type SecretReference struct {
	Store uuid.UUID `json:"store"`
	Path  string    `json:"path"`
	Key   string    `json:"key"`
}

// SecretStore is a secret store of a project: where the project keeps
// secrets that it does not give to Aviary to store.
type SecretStore struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	// Kind is the kind of the store, which says how it is asked.
	Kind string
	// Settings is what the kind needs to know of the store, without what is
	// secret.
	Settings json.RawMessage
	// Credential is what the Supervisor logs in to the store with, as it is
	// stored: sealed for the place that SecretStoreCredentialPlace names.
	Credential []byte
	CreatedAt  time.Time
	// UpdatedAt is when the store was last given how it is reached.
	UpdatedAt time.Time
}

// SecretStoreCredentialPlace names the place of a secret store's credential
// among the secrets that are stored.
func SecretStoreCredentialPlace(id uuid.UUID) string {
	return "secret_stores/" + id.String() + "/credential"
}

// ProviderKeyPlace names the place of a provider's API key among the secrets
// that are stored. A secret is sealed for its place and opens nowhere else.
func ProviderKeyPlace(id uuid.UUID) string {
	return "providers/" + id.String() + "/api_key"
}

// Turn is one message to a session and the work the agent does in reply.
type Turn struct {
	ID        uuid.UUID `json:"id"`
	SessionID uuid.UUID `json:"session_id"`
	Seq       int       `json:"seq"`
	State     string    `json:"state"`
	Text      string    `json:"text"`
	// CreatedBy is the user who sent the message. It is uuid.Nil for a turn
	// of an installation that asks nobody who they are.
	CreatedBy uuid.UUID `json:"created_by,omitzero"`
	CreatedAt time.Time `json:"created_at"`
	// StartedAt is when the harness was given the message, once it has been.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// StopReason is why the harness ended the turn, once it has.
	StopReason *string    `json:"stop_reason,omitempty"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	// Usage is the token usage of the turn, if the harness reported it.
	Usage *Usage `json:"usage,omitempty"`
	// CancelRequestedAt is when the turn was asked to stop, if it was. A
	// turn that had been dispatched by then ends once its harness has
	// stopped.
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	// Failure is why the turn failed, if it did.
	Failure *Failure `json:"failure,omitempty"`
}

// Failure is why a turn failed.
type Failure struct {
	// Reason is a word for the kind of failure, one of the Failure
	// constants.
	Reason string `json:"reason"`
	// Message is what is known of this failure, for a person to read.
	Message string `json:"message,omitempty"`
}

// The kinds of failure of a turn.
const (
	// FailureHarness: the harness could not be started or answered with an
	// error. That is also how a harness reports that the model endpoint
	// refused it.
	FailureHarness = "harness_error"
	// FailureStartup: the harness did not get the message in time, for
	// example because the sandbox of the session could not be made.
	FailureStartup = "startup_timed_out"
	// FailureResume: the harness could not continue its session, because
	// what it had kept of the conversation is gone or cannot be read. The
	// session keeps its harness session, and its next turn tries again.
	FailureResume = "resume_failed"
	// FailureTimedOut: the turn ran for longer than a turn may.
	FailureTimedOut = "timed_out"
	// FailureSandboxLost: the sandbox of the session is gone, and what the
	// agent had in it.
	FailureSandboxLost = "sandbox_lost"
	// FailureSessionExpired: the session was not used for longer than the
	// installation keeps the sandbox of one. The sandbox is gone with what
	// the agent had in it, and the session has ended.
	FailureSessionExpired = "session_expired"
	// FailureRunner: the runner failed in another way.
	FailureRunner = "runner_error"
)

// Usage is the token usage of a turn as its harness reported it. It is not a
// measurement: harnesses differ in what they count. One reports the sum of
// the model requests of the turn, another only the last request.
type Usage struct {
	TotalTokens       int64  `json:"total_tokens"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	ThoughtTokens     *int64 `json:"thought_tokens,omitempty"`
	CachedReadTokens  *int64 `json:"cached_read_tokens,omitempty"`
	CachedWriteTokens *int64 `json:"cached_write_tokens,omitempty"`
}

// DispatchedTurn is a turn that was dispatched and has not ended.
type DispatchedTurn struct {
	ID uuid.UUID
	// Cancelling is how long ago the turn was asked to stop, or zero if it
	// was not.
	Cancelling time.Duration
}

// FollowedTurn is a turn as the Supervisor that follows it gets it, with
// what it takes to run the turn.
type FollowedTurn struct {
	Turn
	AgentID   uuid.UUID
	ProjectID uuid.UUID
	// Harness, Instructions and Approval are those of the session's agent.
	Harness      string
	Instructions string
	Approval     string
	// ProviderURL is the URL of the agent's provider and Model the name of
	// the agent's model, if the agent names its model.
	ProviderURL string
	Model       string
	// Sandbox is the sandbox of the session, if it has one.
	Sandbox string
	// Dispatched is how long ago the turn was dispatched.
	Dispatched time.Duration
}

// Dispatch is a turn that was dispatched.
type Dispatch struct {
	TurnID uuid.UUID
	// Harness is the harness of the turn's agent.
	Harness string
	// Waited is how long the turn waited to run.
	Waited time.Duration
}

// Origin is where a request of a sandbox comes from: the turn that runs in
// it, and the session, the agent and the project that the turn belongs to.
type Origin struct {
	TurnID    uuid.UUID
	SessionID uuid.UUID
	AgentID   uuid.UUID
	ProjectID uuid.UUID
	// UserID is the user who sent the message of the turn, and Subject is
	// what the identity provider knows that user by. They are uuid.Nil and
	// empty if nobody did who was asked who they are.
	UserID  uuid.UUID
	Subject string
	// Provider is the provider of the agent as it is now, if the agent
	// names its model: its ID, its URL and its API key, as it is stored or
	// where it is.
	Provider *Provider
	// SecretStore is the secret store that the key of the provider is in,
	// as the store is now, if the key is in one.
	SecretStore *SecretStore
}

// Approval is what the harness of a turn asked a person before it went on.
type Approval struct {
	// Request is the request as the runner reported it.
	Request json.RawMessage
	// Answered tells whether a person has chosen an option, and Resolved
	// whether the harness has its answer.
	Answered, Resolved bool
	// AnsweredBy is the user who chose the option. It is uuid.Nil if nobody
	// has, or nobody who was asked who they are.
	AnsweredBy uuid.UUID
}

// Answer is the option that a person chose for a request for approval, on its
// way to the harness.
type Answer struct {
	TurnID       uuid.UUID
	ID, OptionID string
}

// OptionKind returns the kind of the request's option with the ID, as the
// harness gave it, or nothing if the request has no such option.
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

// Store is the records, in a database. Every Supervisor of an installation
// has the same database, and several of them may ask the same of it at
// once: what an operation promises holds then too. A plugin gives the
// Supervisor the store of a database of one kind.
//
// What a store is given as JSON it returns as the same JSON, which need not
// be the same text. An operation whose context is done fails, and has done
// nothing.
type Store interface {
	// Close ends the connection to the database.
	Close()

	// CreateProject creates a project with the name.
	CreateProject(ctx context.Context, name string) (Project, error)

	// Project, like the other operations that read one record by its ID,
	// returns ErrNotFound if there is none.
	Project(ctx context.Context, id uuid.UUID) (Project, error)

	// Projects returns the projects in the order in which they were created.
	Projects(ctx context.Context) ([]Project, error)

	// ProjectsOf returns the projects of which the user, or one of the
	// groups, is a member, in the order in which they were created.
	ProjectsOf(ctx context.Context, userID uuid.UUID, groups []string) ([]Project, error)

	// UserOf returns the user whom the identity provider knows by the
	// subject, and makes the user if this is the first that is seen of
	// them: of the Supervisors that see a user first at once, one makes the
	// user. The user has the display name that is given, and the username
	// if one is given: a user that is given none keeps the one that it has.
	// No two users have the same username. The provider gives a username to
	// one user at a time, so a user that is given the username of another
	// has it from then on, and the other has none. Where a project awaits
	// a member under the username that a user is given, the user is that
	// member from then on.
	UserOf(ctx context.Context, subject, username, displayName string) (User, error)

	// AddMember makes a member of the project of the one who is given: the
	// user with the username, or the group. The member has the role that is
	// given, or the permissions if no role is given, as the user by gave
	// them or as nobody did if by is uuid.Nil. A user with the username who has
	// been seen is the member. Of someone who has not, the username is
	// kept, and the user who is seen with it is the member: if that user
	// is a member of the project by then, the user is the member that was
	// awaited, and not the other any more. It returns ErrNotFound if there
	// is no such project, and ErrMemberExists if the project has the user,
	// the username or the group as a member already.
	AddMember(ctx context.Context, member Member, by uuid.UUID) (Member, error)

	// SetMember gives the member the role, or the permissions if no role is
	// given, as the user by or as nobody.
	SetMember(ctx context.Context, id uuid.UUID, role string, permissions []string, by uuid.UUID) (Member, error)

	// RemoveMember makes the member no member of its project.
	RemoveMember(ctx context.Context, id uuid.UUID) error

	// Members returns the members of the project, in the order in which
	// they were added.
	Members(ctx context.Context, projectID uuid.UUID) ([]Member, error)

	// Permissions returns what the user and the groups may do in the
	// project, each permission once and in the order of their names: what
	// the members of the project that are the user or one of the groups may
	// do together. A member with a role may do what the role may now. There
	// are none if neither the user nor a group is a member.
	Permissions(ctx context.Context, projectID, userID uuid.UUID, groups []string) ([]string, error)

	// Roles returns the roles in the order of their names. A database has
	// the roles owner, editor, user, viewer and auditor from the start, and
	// no others.
	Roles(ctx context.Context) ([]Role, error)

	// SetRole gives the role with the name these permissions, in this
	// order, as the user by or as nobody if by is uuid.Nil. It returns
	// ErrNotFound if there is no such role.
	SetRole(ctx context.Context, name string, permissions []string, by uuid.UUID) (Role, error)

	// CreateAgent creates an agent with what the given one has of its own: its
	// harness, instructions, approval and model. It returns ErrNotFound if the
	// project does not exist, and creates no agent that names a provider that
	// there is not.
	CreateAgent(ctx context.Context, projectID uuid.UUID, agent Agent) (Agent, error)

	// Agent returns the agent with the ID.
	Agent(ctx context.Context, id uuid.UUID) (Agent, error)

	// Agents returns the agents of the project, in the order in which they
	// were created.
	Agents(ctx context.Context, projectID uuid.UUID) ([]Agent, error)

	// CreateSession creates a session of the agent for the user who creates
	// it, or for nobody if by is uuid.Nil. It returns ErrNotFound if the
	// agent does not exist.
	CreateSession(ctx context.Context, agentID, by uuid.UUID) (Session, error)

	// Session returns the session with the ID.
	Session(ctx context.Context, id uuid.UUID) (Session, error)

	// SetSandbox records that the session runs in the sandbox to from now
	// on, where it ran in from before. Either is "" for none: a session is
	// given its first sandbox from "", and loses its sandbox to "". It
	// reports false, and records nothing, for a session that does not have
	// the sandbox from: another Supervisor has given it one meanwhile, or
	// taken that one away. It returns ErrNotFound if there is no such
	// session.
	SetSandbox(ctx context.Context, sessionID uuid.UUID, from, to string) (bool, error)

	// EndSession ends the session: from then on it takes no message. It
	// returns the session as it then is, with the sandbox that it has. A
	// session that had ended before is returned as it is. It returns
	// ErrNotFound if there is no such session, and ErrSessionBusy if a turn
	// of the session has not ended.
	EndSession(ctx context.Context, id uuid.UUID) (Session, error)

	// Sessions returns the sessions of the agent whose IDs are below the ID
	// before, newest first and at most limit of them: those of the user by,
	// or of everyone if by is uuid.Nil. The ID of a session rises with the
	// time at which the session is created, so the newest session has the
	// greatest ID, and the ID of the last session of one answer is where
	// the next goes on.
	Sessions(ctx context.Context, agentID, by, before uuid.UUID, limit int) ([]Session, error)

	// CreateProvider creates the provider as it is given, with its ID: the key
	// of a provider is sealed for that ID before it is stored. It returns
	// ErrNotFound if the project does not exist, and creates no provider whose
	// key is in a secret store that there is not.
	CreateProvider(ctx context.Context, provider Provider) (Provider, error)

	// Provider returns the provider with the ID, its key as it is stored.
	Provider(ctx context.Context, id uuid.UUID) (Provider, error)

	// ReplaceProvider gives the provider with the ID of the given one its name,
	// its URL and its key, stored or in a secret store, in place of all that it
	// had: a provider that is given no key has none from then on. It returns
	// ErrNotFound if there is no such provider.
	ReplaceProvider(ctx context.Context, provider Provider) (Provider, error)

	// CreateSecretStore creates the secret store as it is given, with its ID:
	// the credential of a store is sealed for that ID before it is stored. It
	// returns ErrNotFound if the project does not exist.
	CreateSecretStore(ctx context.Context, secrets SecretStore) (SecretStore, error)

	// SecretStore returns the secret store with the ID, its credential as it
	// is stored.
	SecretStore(ctx context.Context, id uuid.UUID) (SecretStore, error)

	// ReplaceSecretStore gives the secret store with the ID of the given one its
	// name, its kind, its settings and its credential, in place of all that it
	// had. It returns ErrNotFound if there is no such store.
	ReplaceSecretStore(ctx context.Context, secrets SecretStore) (SecretStore, error)

	// CreateTurn queues a message as the session's next turn. It returns
	// ErrNotFound if the session does not exist, ErrSessionEnded if the
	// session has ended, ErrSessionBusy if the session has a turn that has
	// not finished, and ErrQueueFull if more than maxWaiting turns would then
	// wait to run. Zero is no such limit. A queued turn waits only if the
	// turns that run leave it no room among maxRunning. The second result is how many turns then wait to run, the new one among
	// them: zero if it need not wait. The turn is of the user who sent the
	// message, by, or of nobody if by is uuid.Nil.
	CreateTurn(ctx context.Context, sessionID, by uuid.UUID, text string, maxWaiting, maxRunning int) (Turn, int, error)

	// AnnounceQueue calls announce with the turns that wait to run, in the order
	// in which they will run. Only one such call runs at a time, also across
	// Supervisors, so that of two calls the later one has the newer queue: the
	// positions that the turns are told last are the latest.
	AnnounceQueue(ctx context.Context, announce func(queued []uuid.UUID) error) error

	// CancellingTurns returns the turns that were asked to stop and have not
	// ended.
	CancellingTurns(ctx context.Context) ([]uuid.UUID, error)

	// Turn returns the turn with the ID.
	Turn(ctx context.Context, id uuid.UUID) (Turn, error)

	// Turns returns the turns of a session that came before the one with the
	// sequence number before, newest first and at most limit of them.
	Turns(ctx context.Context, sessionID uuid.UUID, before, limit int) ([]Turn, error)

	// TurnItems returns what the agent said and did in each of the turns, in
	// order, by the ID of the turn. A turn has items once it has ended, if the
	// agent said or did anything in it.
	TurnItems(ctx context.Context, turnIDs []uuid.UUID) (map[uuid.UUID][]json.RawMessage, error)

	// DispatchTurns dispatches queued turns, oldest first, for as long as
	// fewer than limit turns are running or about to. It returns the turns
	// that it dispatched.
	DispatchTurns(ctx context.Context, limit int) ([]Dispatch, error)

	// CancelTurn cancels a turn that has not ended. A turn that waits to run
	// ends as cancelled at once. A turn that was dispatched is marked: it ends
	// when its harness has stopped. CancelTurn returns the turn as it then is, and
	// whether the turn had not ended before.
	CancelTurn(ctx context.Context, id uuid.UUID) (Turn, bool, error)

	// ExpireTurns ends the turns that have waited to run for longer than wait
	// as expired, and returns them.
	ExpireTurns(ctx context.Context, wait time.Duration) ([]Turn, error)

	// DispatchedTurns returns the turns that were dispatched and have not ended.
	DispatchedTurns(ctx context.Context) ([]DispatchedTurn, error)

	// FollowTurn returns a turn that was dispatched and has not ended as the
	// Supervisor that follows it gets it: the one that has the turn run and
	// records what comes of it. A turn that is followed runs. Which of the
	// Supervisors follows a turn, they settle in the event log.
	//
	// It returns ErrNotFound if there is no such turn or the turn still
	// waits to run, and ErrTurnEnded if the turn has ended.
	FollowTurn(ctx context.Context, turnID uuid.UUID) (FollowedTurn, error)

	// SandboxTurn returns the turn that runs in the sandbox, and what the turn
	// belongs to: the turn that runs of the session whose sandbox it is. It
	// returns ErrNotFound if no turn runs in the sandbox.
	SandboxTurn(ctx context.Context, sandbox string) (Origin, error)

	// StartTurn records that the harness of the turn has been given the
	// message. It returns how long the turn took to start, from its dispatch,
	// and reports false, and records nothing, for a turn that had started
	// before.
	StartTurn(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error)

	// TurnHarness returns the harness of the agent that the turn belongs to,
	// or ErrNotFound if there is no such turn.
	TurnHarness(ctx context.Context, turnID uuid.UUID) (string, error)

	// SetFinished records how the harness ended the turn: why it stopped and,
	// if the harness reported it, the token usage of the turn.
	SetFinished(ctx context.Context, turnID uuid.UUID, stopReason string, usage *Usage) error

	// EndTurn records that the turn has ended. The turn is completed if it
	// succeeded and the runner reported why the harness stopped, and failed
	// otherwise, for the reason that failure gives. A turn that was asked
	// to stop is cancelled, unless the harness had ended it by itself. From then
	// on the session accepts its next message. The second result is false if the
	// turn had ended before, or does not exist.
	//
	// For a turn that it ends, EndTurn calls items and keeps what it returns as
	// what the agent said and did in the turn. If that fails, the turn has not
	// ended. A database that cannot keep the character U+0000 keeps U+FFFD in
	// its place, here and in a request for approval.
	EndTurn(ctx context.Context, turnID uuid.UUID, succeeded bool, failure Failure,
		items func(context.Context) ([]json.RawMessage, error)) (Turn, bool, error)

	// AddApproval keeps a request of the turn's harness for approval, under the
	// name that the runner gave it. A request that is kept already stays as it
	// is. A turn that there is not has no requests.
	AddApproval(ctx context.Context, turnID uuid.UUID, id string, request json.RawMessage) error

	// ResolveApproval records that the harness has its answer to the request,
	// and how it got it. It returns the request and how long it waited, and
	// reports false for a request that was resolved before or that is not kept.
	ResolveApproval(ctx context.Context, turnID uuid.UUID, id, outcome string) (Approval, time.Duration, bool, error)

	// Approval returns the request of the turn for approval with the ID, or
	// ErrNotFound if the turn made none.
	Approval(ctx context.Context, turnID uuid.UUID, id string) (Approval, error)

	// AnswerApproval keeps the option that a person chose for the request. It
	// reports false for a request that has an answer, that the harness waits for
	// no longer, or whose turn has ended: the first answer stands. The answer
	// is that of the user by, or of nobody if by is uuid.Nil.
	AnswerApproval(ctx context.Context, turnID uuid.UUID, id, optionID string, by uuid.UUID) (bool, error)

	// WaitingApprovals returns the requests of the turn that wait for a person,
	// oldest first.
	WaitingApprovals(ctx context.Context, turnID uuid.UUID) ([]json.RawMessage, error)

	// Answers returns the answers that the harnesses of turns that run have not
	// got yet: those of the turn, or of all turns if it is nil.
	Answers(ctx context.Context, turnID *uuid.UUID) ([]Answer, error)
}

// Plugin connects the Supervisor to the databases of one kind. The address
// of a database is a URL, and its scheme names the kind.
type Plugin interface {
	// Schemes are the schemes of the addresses that the plugin connects to.
	Schemes() []string
	// Open connects to the database at the address. Its schema must be the
	// one that the plugin needs, which Migrate makes it.
	Open(ctx context.Context, address string) (Store, error)
	// Migrate brings the schema of the database at the address up to date. A
	// release migrates once, before it starts its Supervisors.
	Migrate(ctx context.Context, address string) error
	// MigrateTo moves the schema to the version instead, forward or back.
	MigrateTo(ctx context.Context, address string, version int64) error
}

// plugin returns the one of the plugins that connects to the database at the
// address.
func plugin(address string, plugins []Plugin) (Plugin, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		// A URL that cannot be parsed is quoted in the error, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse the URL of the database: %w", err)
	}
	var known []string
	for _, plugin := range plugins {
		if slices.Contains(plugin.Schemes(), parsed.Scheme) {
			return plugin, nil
		}
		known = append(known, plugin.Schemes()...)
	}
	return nil, fmt.Errorf("the URL of the database begins with %q, and this Supervisor was built with the plugins for: %s",
		parsed.Scheme, strings.Join(known, ", "))
}

// Open connects to the database at address, a URL, with the plugin that
// connects to databases with the scheme of the address.
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

// MigrateTo moves the schema of the database at address to the version,
// forward or back.
func MigrateTo(ctx context.Context, address string, version int64, plugins ...Plugin) error {
	plugin, err := plugin(address, plugins)
	if err != nil {
		return err
	}
	return plugin.MigrateTo(ctx, address, version)
}
