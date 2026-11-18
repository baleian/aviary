-- +goose Up
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

-- +goose Down
drop table project_members;
