-- +goose NO TRANSACTION
-- +goose Up
-- An index that was not built whole is dropped first, so that the migration
-- can be run again.
drop index concurrently if exists sessions_left_attachments;
-- For listing the sessions that have left attachments when they ended.
create index concurrently sessions_left_attachments on sessions (ended_at)
    where has_attachments and ended_at is not null;

-- +goose Down
drop index concurrently if exists sessions_left_attachments;
