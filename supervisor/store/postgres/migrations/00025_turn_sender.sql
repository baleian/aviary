-- +goose Up
set local lock_timeout = '5s';
-- The user who sent the message of the turn. A turn of an installation that
-- asks nobody who they are has none.
alter table turns add column created_by uuid references users (id);

-- +goose Down
set local lock_timeout = '5s';
alter table turns drop column created_by;
