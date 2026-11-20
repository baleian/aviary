-- +goose Up
create table projects (
    id uuid primary key,
    name text not null,
    created_at timestamptz not null default now()
);

-- The users of the installation: those whom its identity provider told of
-- when they sent a request.
create table users (
    id uuid primary key,
    -- What the provider knows the user by. It never changes.
    subject text not null unique,
    -- A name of the user that a person can read and that no other user has
    -- at the provider, if the provider gave one.
    username text unique,
    -- What the user is shown as.
    display_name text not null,
    created_at timestamptz not null default now()
);

-- The roles: what a member of a project may do, as a set of permissions
-- under a name. The roles are those that are put in here. An administrator
-- changes the permissions of one, and that holds for every member that has
-- the role.
create table roles (
    name text primary key,
    permissions text[] not null,
    updated_at timestamptz not null default now(),
    -- The user who changed the role last, if somebody has.
    updated_by uuid references users (id)
);
insert into roles (name, permissions) values
    ('owner', '{project.view,agents.manage,connections.manage,sessions.run,sessions.view_all,sessions.control_all}'),
    ('editor', '{project.view,agents.manage,connections.manage,sessions.run}'),
    ('user', '{project.view,sessions.run}'),
    ('viewer', '{project.view}'),
    ('auditor', '{project.view,sessions.view_all}');

-- The members of a project: who may do something in it, and what.
create table project_members (
    id uuid primary key,
    project_id uuid not null references projects (id),
    -- Who the member is, one of three: a user, someone who has not been seen
    -- yet, by the username that they will have, or everyone whom the
    -- identity provider says to be in a group.
    user_id uuid references users (id),
    username text,
    group_name text,
    -- What the member may do, one of two: what a role may, or these
    -- permissions.
    role text references roles (name),
    permissions text[],
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    -- The user who gave the member what it may do, if somebody did who was
    -- asked who they are.
    updated_by uuid references users (id),
    check (num_nonnulls(user_id, username, group_name) = 1),
    check (num_nonnulls(role, permissions) = 1),
    unique (project_id, user_id),
    unique (project_id, username),
    unique (project_id, group_name)
);

-- The secret stores of a project: where the project keeps secrets that it
-- does not give to Aviary to store. A secret of the project, such as the API
-- key of a provider, may be a place in one of them.
create table secret_stores (
    id uuid primary key,
    project_id uuid not null references projects (id),
    name text not null,
    -- The kind of the store, which says how it is asked. The Supervisor has
    -- a plugin for each kind that it knows.
    kind text not null,
    -- How the store is reached, as its kind has it, without what is secret.
    settings jsonb not null,
    -- What the Supervisor logs in to the store with, sealed with a key that
    -- the database does not hold.
    credential bytea not null,
    created_at timestamptz not null default now(),
    -- When the store was last given how it is reached.
    updated_at timestamptz not null default now()
);

-- The model providers of a project: where the model endpoint of each is,
-- and the key to it. An agent of the project names the one that its turns
-- use.
create table providers (
    id uuid primary key,
    project_id uuid not null references projects (id),
    name text not null,
    -- The base URL of the endpoint.
    url text not null,
    -- The API key of the endpoint, sealed with a key that the database does
    -- not hold. An endpoint that takes no key has none, and neither has a
    -- provider whose key is in a secret store of the project: that one has
    -- the store, the path of the secret in it, and the name of the value
    -- among those that the secret holds.
    api_key bytea,
    api_key_store_id uuid references secret_stores (id),
    api_key_path text not null default '',
    api_key_name text not null default '',
    created_at timestamptz not null default now(),
    -- When the URL and the key were last given.
    updated_at timestamptz not null default now()
);

create table agents (
    id uuid primary key,
    project_id uuid not null references projects (id),
    harness text not null,
    -- What the agent is told before every turn, in the words of whoever set
    -- the agent up. An agent need not have any.
    instructions text not null default '',
    -- Who answers when the harness of the agent asks before it acts. 'ask'
    -- is a person, and nothing is asked of an agent without it.
    approval text not null default '',
    -- The model that the turns of the agent use: a provider of the agent's
    -- project, and the name of a model as that provider knows it. An agent
    -- that was created without a model has none.
    provider_id uuid references providers (id),
    model text not null default '',
    created_at timestamptz not null default now()
);

create table sessions (
    id uuid primary key,
    agent_id uuid not null references agents (id),
    -- The user who created the session, whose session it is. A session of an
    -- installation that asks nobody who they are is nobody's.
    created_by uuid references users (id),
    -- The sandbox that the session runs in, as the sandbox service names it.
    sandbox text,
    created_at timestamptz not null default now(),
    -- When the session was ended. A session that has ended takes no message.
    ended_at timestamptz
);

create table turns (
    id uuid primary key,
    session_id uuid not null references sessions (id),
    seq integer not null,
    -- The user who sent the message of the turn. A turn of an installation
    -- that asks nobody who they are has none.
    created_by uuid references users (id),
    state text not null,
    text text not null,
    created_at timestamptz not null default now(),
    -- When the turn was dispatched, when its harness was given the message,
    -- when it was asked to stop, and when it ended. A turn that ended
    -- without a start never reached its harness.
    dispatched_at timestamptz,
    started_at timestamptz,
    cancel_requested_at timestamptz,
    ended_at timestamptz,
    -- Why the harness ended the turn, as the runner reported it.
    stop_reason text,
    -- Why the turn failed: a word for the kind of failure and what is known
    -- of it.
    failure_reason text,
    failure_message text,
    -- The token usage of the turn as its harness reported it. A harness need
    -- not report it, and harnesses differ in what they count, so it is not a
    -- measurement.
    total_tokens bigint,
    input_tokens bigint,
    output_tokens bigint,
    thought_tokens bigint,
    cached_read_tokens bigint,
    cached_write_tokens bigint,
    unique (session_id, seq)
);

-- What the agent said and did in a turn, in the order in which each item
-- began. It is put together from the events of the turn when the turn ends,
-- because the events are kept only for a while. An item is an object with a
-- type: a message or a thought with its whole text, a tool call as it last
-- stood, or the plan as it last stood.
create table turn_items (
    turn_id uuid not null references turns (id),
    seq integer not null,
    item jsonb not null,
    primary key (turn_id, seq)
);

-- What the harness of a turn asked a person before it went on, and the
-- answer. A request waits for as long as its turn runs. It is resolved when
-- the harness has its answer: the option that a person chose or, for a turn
-- that was told to stop first, none.
create table approvals (
    turn_id uuid not null references turns (id),
    -- The name that the runner gave the request among those of its turn.
    id text not null,
    -- The request as the runner reported it: the tool call that the harness
    -- asks about and the options that it takes as an answer.
    request jsonb not null,
    requested_at timestamptz not null default now(),
    -- The option that a person chose, when, and who did. An answer of an
    -- installation that asks nobody who they are is nobody's.
    option_id text,
    answered_at timestamptz,
    answered_by uuid references users (id),
    -- How the harness got its answer, selected or cancelled, and when.
    outcome text,
    resolved_at timestamptz,
    primary key (turn_id, id)
);

-- The oldest Supervisor that still works on this schema, given as the schema
-- version that it was built for. A Supervisor built for an older version
-- does not start. A migration that takes away something that older
-- Supervisors use raises the value.
create table schema_floor (
    -- The table holds one row.
    singleton boolean primary key default true check (singleton),
    version bigint not null
);
insert into schema_floor (version) values (0);

-- The agents of a project and the sessions of an agent are listed: the
-- sessions newest first, in the order of their IDs.
create index agents_project on agents (project_id);
create index sessions_agent on sessions (agent_id, id);
-- A request that leaves a sandbox is told apart by the turn that runs in the
-- sandbox, which is found by the session whose sandbox it is.
create index sessions_sandbox on sessions (sandbox) where sandbox is not null;
-- A session runs one turn at a time.
create unique index turns_unfinished_per_session on turns (session_id) where ended_at is null;
-- The queued turns in the order in which they are dispatched.
create index turns_queued on turns (created_at, id) where state = 'queued';
-- The answers that are on their way to their harnesses, which every
-- Supervisor looks for again and again.
create index approvals_answered on approvals (turn_id) where answered_at is not null and resolved_at is null;

-- +goose Down
drop table schema_floor;
drop table approvals;
drop table turn_items;
drop table turns;
drop table sessions;
drop table agents;
drop table providers;
drop table secret_stores;
drop table project_members;
drop table roles;
drop table users;
drop table projects;
