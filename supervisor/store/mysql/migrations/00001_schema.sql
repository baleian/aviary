-- +goose Up
create table projects (
    id binary(16) primary key,
    name mediumtext not null,
    created_at datetime(6) not null default (now(6))
);

-- The users of the installation: those whom its identity provider told of
-- when they sent a request. The subject is what the provider knows a user
-- by, and the username a name that a person can read and that no other user
-- has at the provider, if the provider gave one. They are compared as they
-- are written.
create table users (
    id binary(16) primary key,
    subject varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    username varchar(255) character set utf8mb4 collate utf8mb4_bin,
    display_name text not null,
    created_at datetime(6) not null default (now(6)),
    unique key (subject),
    unique key (username)
);

-- The roles: what a member of a project may do, as a set of permissions
-- under a name. The roles are those that are put in here. An administrator
-- changes the permissions of one, and that holds for every member that has
-- the role.
create table roles (
    name varchar(64) primary key,
    permissions json not null,
    updated_at datetime(6) not null default (now(6)),
    -- The user who changed the role last, if somebody has.
    updated_by binary(16),
    foreign key (updated_by) references users (id)
);
insert into roles (name, permissions) values
    ('owner', '["project.view", "agents.manage", "connections.manage", "sessions.run", "sessions.view_all", "sessions.control_all"]'),
    ('editor', '["project.view", "agents.manage", "connections.manage", "sessions.run"]'),
    ('user', '["project.view", "sessions.run"]'),
    ('viewer', '["project.view"]'),
    ('auditor', '["project.view", "sessions.view_all"]');

-- The members of a project: who may do something in it, and what. A member
-- is one of three: a user, someone who has not been seen yet, by the
-- username that they will have, or everyone whom the identity provider says
-- to be in a group. What a member may do is one of two: what a role may, or
-- the permissions of its own. The user who gave the member that is kept, if
-- somebody did who was asked who they are.
create table project_members (
    id binary(16) primary key,
    project_id binary(16) not null,
    user_id binary(16),
    username varchar(255) character set utf8mb4 collate utf8mb4_bin,
    group_name varchar(255) character set utf8mb4 collate utf8mb4_bin,
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

-- The secret stores of a project: where the project keeps secrets that it
-- does not give to Aviary to store.
create table secret_stores (
    id binary(16) primary key,
    project_id binary(16) not null,
    name mediumtext not null,
    -- The kind of the store, which says how it is asked.
    kind mediumtext not null,
    -- How the store is reached, as its kind has it, without what is secret.
    settings json not null,
    -- What the Supervisor logs in to the store with, sealed with a key that
    -- the database does not hold.
    credential blob not null,
    created_at datetime(6) not null default (now(6)),
    -- When the store was last given how it is reached.
    updated_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id)
);

-- The model providers of a project: where the model endpoint of each is,
-- and the key to it. An agent of the project names the one that its turns
-- use.
create table providers (
    id binary(16) primary key,
    project_id binary(16) not null,
    name mediumtext not null,
    -- The base URL of the endpoint.
    url mediumtext not null,
    -- The API key of the endpoint, sealed with a key that the database does
    -- not hold. An endpoint that takes no key has none, and neither has a
    -- provider whose key is in a secret store of the project: that one has
    -- the store, the path of the secret in it, and the name of the value
    -- among those that the secret holds.
    api_key blob,
    api_key_store_id binary(16),
    api_key_path mediumtext not null default (''),
    api_key_name mediumtext not null default (''),
    created_at datetime(6) not null default (now(6)),
    -- When the URL and the key were last given.
    updated_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id),
    foreign key (api_key_store_id) references secret_stores (id)
);

create table agents (
    id binary(16) primary key,
    project_id binary(16) not null,
    harness mediumtext not null,
    -- What the agent is told before every turn. An agent need not have any.
    instructions mediumtext not null default (''),
    -- Who answers when the harness of the agent asks before it acts. 'ask'
    -- is a person, and nothing is asked of an agent without it.
    approval varchar(64) not null default '',
    -- The model that the turns of the agent use: a provider of the agent's
    -- project, and the name of a model as that provider knows it.
    provider_id binary(16),
    model mediumtext not null default (''),
    created_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id),
    foreign key (provider_id) references providers (id)
);

create table sessions (
    id binary(16) primary key,
    agent_id binary(16) not null,
    -- The user who created the session, whose session it is. A session of an
    -- installation that asks nobody who they are is nobody's.
    created_by binary(16),
    -- The sandbox that the session runs in.
    sandbox varchar(255),
    created_at datetime(6) not null default (now(6)),
    -- When the session was ended. A session that has ended takes no message.
    ended_at datetime(6),
    foreign key (agent_id) references agents (id),
    foreign key (created_by) references users (id)
);

create table turns (
    id binary(16) primary key,
    session_id binary(16) not null,
    seq int not null,
    -- The user who sent the message of the turn. A turn of an installation
    -- that asks nobody who they are has none.
    created_by binary(16),
    state varchar(32) not null,
    text mediumtext not null,
    created_at datetime(6) not null default (now(6)),
    -- When the turn was dispatched, when its harness was given the message,
    -- when it was asked to stop, and when it ended.
    dispatched_at datetime(6),
    started_at datetime(6),
    cancel_requested_at datetime(6),
    ended_at datetime(6),
    -- Why the harness ended the turn, as the runner reported it.
    stop_reason mediumtext,
    -- Why the turn failed: a word for the kind of failure and what is known
    -- of it.
    failure_reason varchar(64),
    failure_message mediumtext,
    -- The token usage of the turn as its harness reported it.
    total_tokens bigint,
    input_tokens bigint,
    output_tokens bigint,
    thought_tokens bigint,
    cached_read_tokens bigint,
    cached_write_tokens bigint,
    foreign key (session_id) references sessions (id),
    foreign key (created_by) references users (id),
    unique key turns_session_seq (session_id, seq),
    -- A session runs one turn at a time: no two of its turns are without an
    -- end. A turn that has ended counts for nothing here.
    unique key turns_unfinished_per_session ((case when ended_at is null then session_id end)),
    -- The turns of a state in the order in which they are dispatched.
    key turns_by_state (state, created_at, id)
);

-- What the agent said and did in a turn, in the order in which each item
-- began. It is put together from the events of the turn when the turn ends,
-- because the events are kept only for a while.
create table turn_items (
    turn_id binary(16) not null,
    seq int not null,
    item json not null,
    primary key (turn_id, seq),
    foreign key (turn_id) references turns (id)
);

-- What the harness of a turn asked a person before it went on, and the
-- answer. A request waits for as long as its turn runs. It is resolved when
-- the harness has its answer: the option that a person chose or, for a turn
-- that was told to stop first, none.
create table approvals (
    turn_id binary(16) not null,
    -- The name that the runner gave the request among those of its turn,
    -- which is the same name only if it is the same letter for letter.
    id varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    -- The request as the runner reported it.
    request json not null,
    requested_at datetime(6) not null default (now(6)),
    -- The option that a person chose, when, and who did. An answer of an
    -- installation that asks nobody who they are is nobody's.
    option_id mediumtext,
    answered_at datetime(6),
    answered_by binary(16),
    -- How the harness got its answer, selected or cancelled, and when.
    outcome varchar(64),
    resolved_at datetime(6),
    primary key (turn_id, id),
    foreign key (turn_id) references turns (id),
    foreign key (answered_by) references users (id)
);

-- The oldest Supervisor that still works on this schema, given as the schema
-- version that it was built for. A Supervisor built for an older version
-- does not start. A migration that takes away something that older
-- Supervisors use raises the value.
create table schema_floor (
    -- The table holds one row.
    singleton tinyint(1) primary key default 1,
    version bigint not null,
    check (singleton = 1)
);
insert into schema_floor (version) values (0);

-- What only one Supervisor does at a time. A Supervisor holds the row of it
-- for as long as its transaction lasts, and the others wait for the row:
-- dispatch while turns are dispatched, and queue while the turns that wait
-- are counted or told their positions.
create table locks (
    name varchar(32) primary key
);
insert into locks (name) values ('dispatch'), ('queue');

-- +goose Down
drop table locks;
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
