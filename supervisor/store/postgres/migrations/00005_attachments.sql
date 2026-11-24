-- +goose Up
-- A file that a message to a session carries, or that was uploaded for a
-- message to carry. Its bytes are an object of the depot.
create table attachments (
    id uuid primary key,
    session_id uuid not null references sessions (id),
    -- The turn of the message that carries the file, and the place of the
    -- file among those of the message. Null until a message carries it.
    turn_id uuid references turns (id),
    position integer,
    -- The files of a message have different names.
    name text not null,
    media_type text not null,
    size bigint not null,
    -- Who uploaded the file. Null if the request was not authenticated.
    created_by uuid references users (id),
    created_at timestamptz not null default now()
);
-- For the files of a turn.
create index attachments_turn on attachments (turn_id) where turn_id is not null;
-- For the files that no message carries, by their age.
create index attachments_unsent on attachments (created_at) where turn_id is null;

-- +goose Down
drop table attachments;
