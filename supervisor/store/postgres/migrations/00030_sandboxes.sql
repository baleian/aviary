-- +goose Up
set local lock_timeout = '5s';

-- The sandbox that the session runs in, as the sandbox service names it.
alter table sessions add column sandbox text;

-- +goose Down
set local lock_timeout = '5s';

alter table sessions drop column sandbox;
