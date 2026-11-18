-- +goose Up
-- The user who created the session, whose session it is. A session of an
-- installation that asks nobody who they are is nobody's.
alter table sessions add column created_by binary(16),
    add constraint sessions_created_by foreign key (created_by) references users (id);

-- +goose Down
alter table sessions drop foreign key sessions_created_by, drop column created_by;
