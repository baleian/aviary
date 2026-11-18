-- +goose Up
set local lock_timeout = '5s';
-- The user who chose the option. An answer of an installation that asks
-- nobody who they are is nobody's.
alter table approvals add column answered_by uuid references users (id);

-- +goose Down
set local lock_timeout = '5s';
alter table approvals drop column answered_by;
