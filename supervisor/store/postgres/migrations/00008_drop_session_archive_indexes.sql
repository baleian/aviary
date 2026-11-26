-- +goose NO TRANSACTION
-- +goose Up
-- Sessions keep their homes in their sandboxes: none is an archive.
drop index concurrently if exists sessions_left_archives;
drop index concurrently if exists sessions_archived;

-- +goose Down
drop index concurrently if exists sessions_archived;
create index concurrently sessions_archived on sessions (archived_at)
    where archive is not null and sandbox is null and ended_at is null;
drop index concurrently if exists sessions_left_archives;
create index concurrently sessions_left_archives on sessions (archived_at)
    where archive is not null and ended_at is not null;
