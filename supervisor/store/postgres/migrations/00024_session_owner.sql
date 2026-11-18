-- +goose Up
set local lock_timeout = '5s';
-- The user who created the session, whose session it is. A session of an
-- installation that asks nobody who they are is nobody's.
alter table sessions add column created_by uuid references users (id);

-- +goose Down
set local lock_timeout = '5s';
alter table sessions drop column created_by;
