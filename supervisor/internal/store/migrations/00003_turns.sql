-- +goose Up
create table turns (
    id uuid primary key,
    session_id uuid not null references sessions (id),
    seq integer not null,
    state text not null,
    text text not null,
    created_at timestamptz not null default now(),
    ended_at timestamptz,
    unique (session_id, seq)
);

-- A session runs one turn at a time.
create unique index turns_unfinished_per_session on turns (session_id) where ended_at is null;

-- +goose Down
drop table turns;
