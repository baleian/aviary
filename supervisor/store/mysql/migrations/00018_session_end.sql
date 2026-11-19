-- +goose Up
-- When the session was ended. A session that has ended takes no message.
alter table sessions add column ended_at datetime(6);

-- +goose Down
alter table sessions drop column ended_at;
