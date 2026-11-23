-- +goose Up
create table projects (
    id uuid primary key,
    slug text not null,
    name text not null default '',
    created_at timestamptz not null default now(),
    constraint projects_slug unique (slug)
);

-- Users as authenticated by the identity provider.
create table users (
    id uuid primary key,
    -- The provider's stable identifier for the user.
    subject text not null unique,
    -- A human-readable name, if the provider gives one.
    username text unique,
    display_name text not null,
    created_at timestamptz not null default now()
);

-- The predefined roles. Administrators can change the permissions of a role,
-- which then apply to every member that has it.
create table roles (
    name text primary key,
    permissions text[] not null,
    updated_at timestamptz not null default now(),
    updated_by uuid references users (id)
);
insert into roles (name, permissions) values
    ('owner', '{project.view,agents.manage,connections.manage,sessions.run,sessions.view_all,sessions.control_all}'),
    ('editor', '{project.view,agents.manage,connections.manage,sessions.run}'),
    ('user', '{project.view,sessions.run}'),
    ('viewer', '{project.view}'),
    ('auditor', '{project.view,sessions.view_all}');

create table project_members (
    id uuid primary key,
    project_id uuid not null references projects (id),
    -- One of: a user, the username of an awaited user who is not known yet,
    -- or a group of the identity provider.
    user_id uuid references users (id),
    username text,
    group_name text,
    -- One of: a role, or permissions of the member's own.
    role text references roles (name),
    permissions text[],
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    updated_by uuid references users (id),
    check (num_nonnulls(user_id, username, group_name) = 1),
    check (num_nonnulls(role, permissions) = 1),
    unique (project_id, user_id),
    unique (project_id, username),
    unique (project_id, group_name)
);

-- External secret stores in which projects keep secrets, such as provider
-- API keys, outside Aviary.
create table secret_stores (
    id uuid primary key,
    project_id uuid not null references projects (id),
    name text not null,
    -- Selects the Supervisor's secret store plugin for the store.
    kind text not null,
    -- The settings of the store, without secrets.
    settings jsonb not null,
    -- Encrypted with a key that the database does not hold.
    credential bytea not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Model providers: the model endpoints of a project and their API keys.
create table providers (
    id uuid primary key,
    project_id uuid not null references projects (id),
    name text not null,
    -- The base URL of the model endpoint.
    url text not null,
    -- The headers that the requests to the endpoint carry, as a JSON array.
    -- Each is {"name": "...", "value": "..."}, {"name": "...", "sealed":
    -- "..."} with a secret encrypted like the API key, or {"name": "...",
    -- "at": {"store": "...", "path": "...", "key": "..."}} for a secret in a
    -- secret store. It is null if there are none.
    headers jsonb,
    -- The API key, encrypted with a key that the database does not hold. It
    -- is null if the endpoint takes no key, or if the key is in a secret
    -- store: then the key is located by the store, the path of a secret in
    -- it, and the name of the value within the secret.
    api_key bytea,
    api_key_store_id uuid references secret_stores (id),
    api_key_path text not null default '',
    api_key_name text not null default '',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- MCP servers: the MCP servers of a project, whose tools its agents use.
create table mcp_servers (
    id uuid primary key,
    project_id uuid not null references projects (id),
    -- Names the server in the project, and its tools to a harness. It does
    -- not change.
    slug text not null,
    name text not null default '',
    url text not null,
    -- 'http' for Streamable HTTP, or 'sse'.
    transport text not null,
    -- The headers that the requests to the server carry, as those of a
    -- provider are kept, with a secret encrypted for the server.
    headers jsonb,
    -- The tools that the server last listed, as a JSON array of {"name":
    -- "...", "description": "..."}, and when. Both are null if it has not
    -- listed them.
    tools jsonb,
    tools_listed_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint mcp_servers_project_slug unique (project_id, slug)
);

create table agents (
    id uuid primary key,
    project_id uuid not null references projects (id),
    slug text not null,
    name text not null default '',
    description text not null default '',
    harness text not null,
    -- Given to the agent before every turn, if not empty.
    instructions text not null default '',
    -- 'ask' if the harness asks a person for approval before it acts, empty
    -- if it does not ask.
    approval text not null default '',
    -- The agent's model: a provider of the project and the provider's name
    -- for the model.
    provider_id uuid not null references providers (id),
    model text not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    -- Set if the agent is deleted.
    deleted_at timestamptz
);

-- A slug is unique among the agents of a project that are not deleted. Also
-- serves listing them.
create unique index agents_project_slug on agents (project_id, slug) where deleted_at is null;

-- The MCP servers that an agent begins its sessions with.
create table agent_mcp_servers (
    agent_id uuid not null references agents (id),
    mcp_server_id uuid not null references mcp_servers (id) on delete cascade,
    primary key (agent_id, mcp_server_id)
);

create index agent_mcp_servers_server on agent_mcp_servers (mcp_server_id);

create table sessions (
    id uuid primary key,
    agent_id uuid not null references agents (id),
    title text not null default '',
    -- The owner of the session. Null if the request was not authenticated.
    created_by uuid references users (id),
    -- The name of the session's sandbox, if it has one.
    sandbox text,
    -- 'awake', 'asleep' or 'lost', if the session has a sandbox.
    sandbox_state text,
    created_at timestamptz not null default now(),
    ended_at timestamptz,
    -- 'expired' if the session ended because it expired. Null if a caller
    -- ended it.
    end_reason text
);

-- The MCP servers that a session turned on or off where it differs from its
-- agent. Its turns have the servers of the agent but for these.
create table session_mcp_servers (
    session_id uuid not null references sessions (id),
    mcp_server_id uuid not null references mcp_servers (id) on delete cascade,
    enabled boolean not null,
    primary key (session_id, mcp_server_id)
);

create index session_mcp_servers_server on session_mcp_servers (mcp_server_id);

create table turns (
    id uuid primary key,
    session_id uuid not null references sessions (id),
    seq integer not null,
    -- The sender of the message. Null if the request was not authenticated.
    created_by uuid references users (id),
    state text not null,
    text text not null,
    created_at timestamptz not null default now(),
    -- started_at is when the harness received the message. A turn that
    -- ended without it never reached its harness.
    dispatched_at timestamptz,
    started_at timestamptz,
    cancel_requested_at timestamptz,
    ended_at timestamptz,
    -- Why the harness ended the turn, as the runner reported it.
    stop_reason text,
    failure_reason text,
    failure_message text,
    -- The token usage as the harness reported it, if it did. Harnesses differ
    -- in what they count.
    total_tokens bigint,
    input_tokens bigint,
    output_tokens bigint,
    thought_tokens bigint,
    cached_read_tokens bigint,
    cached_write_tokens bigint,
    unique (session_id, seq)
);

-- What the agent said and did in a turn, in the order in which each item
-- began. The items are assembled from the turn's events when it ends, since
-- the events are kept only for a while. An item is an object with a type: a
-- message or a thought with its full text, or the final state of a tool call
-- or of the plan.
create table turn_items (
    turn_id uuid not null references turns (id),
    seq integer not null,
    item jsonb not null,
    primary key (turn_id, seq)
);

-- Approval requests of a turn's harness and their answers. A request waits
-- while its turn runs. It is resolved when the harness has its answer: the
-- option that a person chose or, if the turn was cancelled first, none.
create table approvals (
    turn_id uuid not null references turns (id),
    -- The ID that the runner gave the request within its turn.
    id text not null,
    -- The tool call that the harness asks about and the options it accepts.
    request jsonb not null,
    requested_at timestamptz not null default now(),
    -- answered_by is null if the request was not authenticated.
    option_id text,
    answered_at timestamptz,
    answered_by uuid references users (id),
    -- How the request was resolved: 'selected' or 'cancelled'.
    outcome text,
    resolved_at timestamptz,
    primary key (turn_id, id)
);

-- The oldest schema version that a Supervisor may be built for. Older
-- Supervisors do not start. A migration that removes something that they use
-- raises it.
create table schema_floor (
    -- Restricts the table to one row.
    singleton boolean primary key default true check (singleton),
    version bigint not null
);
insert into schema_floor (version) values (0);

-- For listing the sessions of an agent by ID.
create index sessions_agent on sessions (agent_id, id);
-- For listing the sessions that have a sandbox.
create index sessions_with_sandbox on sessions (id) where sandbox is not null;
-- A session has at most one unfinished turn.
create unique index turns_unfinished_per_session on turns (session_id) where ended_at is null;
-- The queue, in dispatch order.
create index turns_queued on turns (created_at, id) where state = 'queued';
-- The answers not yet delivered to their harnesses, which every Supervisor
-- polls.
create index approvals_answered on approvals (turn_id) where answered_at is not null and resolved_at is null;

-- +goose Down
drop table schema_floor;
drop table approvals;
drop table turn_items;
drop table turns;
drop table session_mcp_servers;
drop table sessions;
drop table agent_mcp_servers;
drop table agents;
drop table mcp_servers;
drop table providers;
drop table secret_stores;
drop table project_members;
drop table roles;
drop table users;
drop table projects;
