-- +goose Up
set local lock_timeout = '5s';

-- The runner that took the turn, by the UUID that it made up for itself. A
-- turn that runs is given again only to that runner.
alter table turns add column runner_id uuid;

-- +goose Down
set local lock_timeout = '5s';

alter table turns drop column runner_id;
