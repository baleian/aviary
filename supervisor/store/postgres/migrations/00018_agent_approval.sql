-- +goose Up
set local lock_timeout = '5s';

-- Who answers when the harness of the agent asks before it acts. 'ask' is a
-- person. An agent has '' unless it was created with another value: nothing
-- is asked then, as before there was this column.
alter table agents add column approval text not null default '';

-- +goose Down
set local lock_timeout = '5s';

alter table agents drop column approval;
