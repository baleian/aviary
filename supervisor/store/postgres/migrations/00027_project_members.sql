-- +goose Up
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

-- +goose Down
drop table project_members;
