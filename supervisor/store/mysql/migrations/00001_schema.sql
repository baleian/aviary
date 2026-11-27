-- +goose Up
create table projects (
    id binary(16) primary key,
    -- Compared exactly, not case-insensitively.
    slug varchar(63) character set utf8mb4 collate utf8mb4_bin not null,
    name mediumtext not null default (''),
    created_at datetime(6) not null default (now(6)),
    unique key projects_slug (slug)
);

-- Users as authenticated by the identity provider. Subjects and usernames are
-- compared exactly, not case-insensitively.
create table users (
    id binary(16) primary key,
    -- The provider's stable identifier for the user.
    subject varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    -- A human-readable name, if the provider gives one.
    username varchar(255) character set utf8mb4 collate utf8mb4_bin,
    display_name text not null,
    created_at datetime(6) not null default (now(6)),
    unique key (subject),
    unique key (username)
);

-- The predefined roles. Administrators can change the permissions of a role,
-- which then apply to every member that has it.
create table roles (
    name varchar(64) primary key,
    permissions json not null,
    updated_at datetime(6) not null default (now(6)),
    updated_by binary(16),
    foreign key (updated_by) references users (id)
);
insert into roles (name, permissions) values
    ('owner', '["project.view", "agents.manage", "connections.manage", "sessions.run", "sessions.view_all", "sessions.control_all"]'),
    ('editor', '["project.view", "agents.manage", "connections.manage", "sessions.run"]'),
    ('user', '["project.view", "sessions.run"]'),
    ('viewer', '["project.view"]'),
    ('auditor', '["project.view", "sessions.view_all"]');

create table project_members (
    id binary(16) primary key,
    project_id binary(16) not null,
    -- One of: a user, the username of an awaited user who is not known yet,
    -- or a group of the identity provider.
    user_id binary(16),
    username varchar(255) character set utf8mb4 collate utf8mb4_bin,
    group_name varchar(255) character set utf8mb4 collate utf8mb4_bin,
    -- One of: a role, or permissions of the member's own.
    role varchar(64),
    permissions json,
    created_at datetime(6) not null default (now(6)),
    updated_at datetime(6) not null default (now(6)),
    updated_by binary(16),
    foreign key (project_id) references projects (id),
    foreign key (user_id) references users (id),
    foreign key (role) references roles (name),
    foreign key (updated_by) references users (id),
    check ((user_id is not null) + (username is not null) + (group_name is not null) = 1),
    check ((role is not null) + (permissions is not null) = 1),
    unique key (project_id, user_id),
    unique key (project_id, username),
    unique key (project_id, group_name)
);

-- External secret stores in which projects keep secrets, such as provider
-- API keys, outside Aviary.
create table secret_stores (
    id binary(16) primary key,
    project_id binary(16) not null,
    name mediumtext not null,
    -- Selects the Supervisor's secret store plugin for the store.
    kind mediumtext not null,
    -- The settings of the store, without secrets.
    settings json not null,
    -- Encrypted with a key that the database does not hold.
    credential blob not null,
    created_at datetime(6) not null default (now(6)),
    updated_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id)
);

-- Model providers: the model endpoints of a project and their API keys.
create table providers (
    id binary(16) primary key,
    project_id binary(16) not null,
    name mediumtext not null,
    -- The base URL of the model endpoint.
    url mediumtext not null,
    -- The headers that the requests to the endpoint carry, as a JSON array.
    -- Each is {"name": "...", "value": "..."}, {"name": "...", "sealed":
    -- "..."} with a secret encrypted like the API key, or {"name": "...",
    -- "at": {"store": "...", "path": "...", "key": "..."}} for a secret in a
    -- secret store. It is null if there are none.
    headers json,
    -- The API key, encrypted with a key that the database does not hold. It
    -- is null if the endpoint takes no key, or if the key is in a secret
    -- store: then the key is located by the store, the path of a secret in
    -- it, and the name of the value within the secret.
    api_key blob,
    api_key_store_id binary(16),
    api_key_path mediumtext not null default (''),
    api_key_name mediumtext not null default (''),
    created_at datetime(6) not null default (now(6)),
    updated_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id),
    foreign key (api_key_store_id) references secret_stores (id)
);

-- MCP servers: the MCP servers of a project, whose tools its agents use.
create table mcp_servers (
    id binary(16) primary key,
    project_id binary(16) not null,
    -- Names the server in the project, and its tools to a harness. It does
    -- not change.
    slug varchar(63) character set utf8mb4 collate utf8mb4_bin not null,
    name mediumtext not null default (''),
    url mediumtext not null,
    -- 'http' for Streamable HTTP, or 'sse'.
    transport varchar(16) not null,
    -- The headers that the requests to the server carry, as those of a
    -- provider are kept, with a secret encrypted for the server.
    headers json,
    -- The tools that the server last listed, as a JSON array of {"name":
    -- "...", "description": "..."}, and when. Both are null if it has not
    -- listed them.
    tools json,
    tools_listed_at datetime(6),
    created_at datetime(6) not null default (now(6)),
    updated_at datetime(6) not null default (now(6)),
    unique key mcp_servers_project_slug (project_id, slug),
    foreign key (project_id) references projects (id)
);

create table agents (
    id binary(16) primary key,
    project_id binary(16) not null,
    -- Compared exactly, not case-insensitively.
    slug varchar(63) character set utf8mb4 collate utf8mb4_bin not null,
    name mediumtext not null default (''),
    description mediumtext not null default (''),
    harness mediumtext not null,
    -- Given to the agent before every turn, if not empty.
    instructions mediumtext not null default (''),
    -- The agent's model: a provider of the project and the provider's name
    -- for the model.
    provider_id binary(16) not null,
    model mediumtext not null,
    created_at datetime(6) not null default (now(6)),
    updated_at datetime(6) not null default (now(6)),
    -- Set if the agent is deleted.
    deleted_at datetime(6),
    foreign key (project_id) references projects (id),
    foreign key (provider_id) references providers (id),
    -- A slug is unique among the agents of a project that are not deleted:
    -- the key has no value for a deleted agent.
    unique key agents_project_slug (project_id, (if(deleted_at is null, slug, null)))
);

-- The MCP servers that an agent begins its sessions with.
create table agent_mcp_servers (
    agent_id binary(16) not null,
    mcp_server_id binary(16) not null,
    primary key (agent_id, mcp_server_id),
    foreign key (agent_id) references agents (id),
    foreign key (mcp_server_id) references mcp_servers (id) on delete cascade
);

create table sessions (
    id binary(16) primary key,
    agent_id binary(16) not null,
    title mediumtext not null default (''),
    -- The owner of the session. Null if the request was not authenticated.
    created_by binary(16),
    -- The name of the session's sandbox, if it has one.
    sandbox varchar(255),
    -- 'awake', 'asleep' or 'lost', if the session has a sandbox.
    sandbox_state varchar(32),
    created_at datetime(6) not null default (now(6)),
    ended_at datetime(6),
    -- 'expired' if the session ended because it expired. Null if a caller
    -- ended it.
    end_reason varchar(32),
    -- Whether the object storage may hold files of the session, which are
    -- deleted after it ends.
    has_attachments boolean not null default false,
    foreign key (agent_id) references agents (id),
    foreign key (created_by) references users (id),
    -- For listing the sessions that have a sandbox.
    key sessions_sandbox (sandbox),
    -- For listing the sessions that have left files when they ended.
    key sessions_left_attachments (has_attachments, ended_at)
);

-- The MCP servers that a session turned on or off where it differs from its
-- agent. Its turns have the servers of the agent but for these.
create table session_mcp_servers (
    session_id binary(16) not null,
    mcp_server_id binary(16) not null,
    enabled boolean not null,
    primary key (session_id, mcp_server_id),
    foreign key (session_id) references sessions (id),
    foreign key (mcp_server_id) references mcp_servers (id) on delete cascade
);

create table turns (
    id binary(16) primary key,
    session_id binary(16) not null,
    seq int not null,
    -- The sender of the message. Null if the request was not authenticated.
    created_by binary(16),
    state varchar(32) not null,
    text mediumtext not null,
    created_at datetime(6) not null default (now(6)),
    -- started_at is when the harness received the message. A turn that
    -- ended without it never reached its harness.
    dispatched_at datetime(6),
    started_at datetime(6),
    cancel_requested_at datetime(6),
    ended_at datetime(6),
    -- Why the harness ended the turn, as the runner reported it.
    stop_reason mediumtext,
    failure_reason varchar(64),
    failure_message mediumtext,
    -- The token usage as the harness reported it, if it did. Harnesses differ
    -- in what they count.
    total_tokens bigint,
    input_tokens bigint,
    output_tokens bigint,
    thought_tokens bigint,
    cached_read_tokens bigint,
    cached_write_tokens bigint,
    foreign key (session_id) references sessions (id),
    foreign key (created_by) references users (id),
    unique key turns_session_seq (session_id, seq),
    -- A session has at most one unfinished turn. Ended turns are indexed as
    -- null, which a unique key allows any number of times.
    unique key turns_unfinished_per_session ((case when ended_at is null then session_id end)),
    -- The turns of each state in dispatch order.
    key turns_by_state (state, created_at, id)
);

-- What the agent said and did in a turn, in the order in which each item
-- began. The items are assembled from the turn's events when it ends, since
-- the events are kept only for a while. An item is an object with a type: a
-- message or a thought with its full text, or the final state of a tool call
-- or of the plan.
create table turn_items (
    turn_id binary(16) not null,
    seq int not null,
    item json not null,
    primary key (turn_id, seq),
    foreign key (turn_id) references turns (id)
);

-- Approval requests of a turn's harness and their answers. A request waits
-- while its turn runs. It is resolved when the harness has its answer: the
-- option that a person chose or, if the turn was cancelled first, none.
create table approvals (
    turn_id binary(16) not null,
    -- The ID that the runner gave the request within its turn, compared
    -- exactly.
    id varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    -- The tool call that the harness asks about and the options it accepts.
    request json not null,
    requested_at datetime(6) not null default (now(6)),
    -- answered_by is null if the request was not authenticated.
    option_id mediumtext,
    answered_at datetime(6),
    answered_by binary(16),
    -- How the request was resolved: 'selected' or 'cancelled'.
    outcome varchar(64),
    resolved_at datetime(6),
    primary key (turn_id, id),
    foreign key (turn_id) references turns (id),
    foreign key (answered_by) references users (id)
);

-- A file that a message to a session carries, or that was uploaded for a
-- message to carry. Its bytes are an object in the object storage.
create table attachments (
    id binary(16) primary key,
    session_id binary(16) not null,
    -- The turn of the message that carries the file, and the place of the
    -- file among those of the message. Null until a message carries it.
    turn_id binary(16),
    position int,
    -- The files of a message have different names. Compared exactly, not
    -- case-insensitively.
    name varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    media_type varchar(255) not null,
    size bigint not null,
    -- Who uploaded the file. Null if the request was not authenticated.
    created_by binary(16),
    created_at datetime(6) not null default (now(6)),
    foreign key (session_id) references sessions (id),
    foreign key (turn_id) references turns (id),
    foreign key (created_by) references users (id),
    -- For the files of a turn, and for those that no message carries by
    -- their age: these have no turn in the key.
    key attachments_turn (turn_id, created_at)
);

-- The oldest schema version that a Supervisor may be built for. Older
-- Supervisors do not start. A migration that removes something that they use
-- raises it.
create table schema_floor (
    -- Restricts the table to one row.
    singleton tinyint(1) primary key default 1,
    version bigint not null,
    check (singleton = 1)
);
insert into schema_floor (version) values (0);

-- Locks that serialize work across Supervisors. A transaction holds a lock by
-- locking its row: queue while it counts the queue or announces it.
create table locks (
    name varchar(32) primary key
);
insert into locks (name) values ('queue');

-- +goose Down
drop table locks;
drop table schema_floor;
drop table attachments;
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
