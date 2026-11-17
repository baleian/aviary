-- +goose Up
create table projects (
    id binary(16) primary key,
    name mediumtext not null,
    created_at datetime(6) not null default (now(6))
);

-- +goose Down
drop table projects;
