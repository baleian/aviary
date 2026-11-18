-- +goose Up
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

-- +goose Down
drop table users;
