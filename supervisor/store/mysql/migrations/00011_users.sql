-- +goose Up
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

-- +goose Down
drop table users;
