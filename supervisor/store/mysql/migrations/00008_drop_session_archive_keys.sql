-- +goose Up
-- Sessions keep their homes in their sandboxes: none is an archive.
alter table sessions drop key sessions_archived, algorithm = inplace, lock = none;

-- +goose Down
alter table sessions
    add key sessions_archived (archived_at, ended_at, sandbox),
    algorithm = inplace, lock = none;
