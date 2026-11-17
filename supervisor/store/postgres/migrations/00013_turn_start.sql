-- +goose Up
set local lock_timeout = '5s';

-- When the harness of the turn was given the message. A turn that ended
-- without it never reached its harness.
alter table turns add column started_at timestamptz;

-- +goose Down
set local lock_timeout = '5s';

alter table turns drop column started_at;
