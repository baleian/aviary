-- +goose NO TRANSACTION
-- +goose Up
-- An index that was not built whole is dropped first, so that the migration
-- can be run again.
drop index concurrently if exists sessions_archived;
-- For counting the archived sessions, and for listing those that were
-- archived long ago.
create index concurrently sessions_archived on sessions (archived_at)
    where archive is not null and sandbox is null and ended_at is null;
drop index concurrently if exists sessions_left_archives;
-- For listing the archives that sessions have left when they ended.
create index concurrently sessions_left_archives on sessions (archived_at)
    where archive is not null and ended_at is not null;

-- +goose Down
drop index concurrently if exists sessions_left_archives;
drop index concurrently if exists sessions_archived;
