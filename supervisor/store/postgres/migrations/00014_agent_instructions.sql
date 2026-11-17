-- +goose Up
set local lock_timeout = '5s';

-- What the agent is told before every turn, in the words of whoever set the
-- agent up. An agent need not have any.
alter table agents add column instructions text not null default '';

-- +goose Down
set local lock_timeout = '5s';

alter table agents drop column instructions;
