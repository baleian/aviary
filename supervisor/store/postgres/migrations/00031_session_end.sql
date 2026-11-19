-- +goose Up
set local lock_timeout = '5s';

-- When the session was ended. A session that has ended takes no message.
alter table sessions add column ended_at timestamptz;

-- +goose Down
set local lock_timeout = '5s';

alter table sessions drop column ended_at;
