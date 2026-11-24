-- +goose Up
-- For counting the archived sessions, and for listing those that were
-- archived long ago and those that have left their archive when they ended:
-- only a session with an archive has a time in the key.
alter table sessions
    add key sessions_archived (archived_at, ended_at, sandbox),
    algorithm = inplace, lock = none;

-- +goose Down
alter table sessions drop key sessions_archived;
