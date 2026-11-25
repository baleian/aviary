-- +goose Up
-- For listing the sessions that have left attachments when they ended.
alter table sessions
    add key sessions_left_attachments (has_attachments, ended_at),
    algorithm = inplace, lock = none;

-- +goose Down
alter table sessions drop key sessions_left_attachments;
