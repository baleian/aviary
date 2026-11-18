-- +goose Up
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

-- +goose Down
drop table roles;
