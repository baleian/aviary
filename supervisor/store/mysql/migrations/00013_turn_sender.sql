-- +goose Up
-- The user who sent the message of the turn. A turn of an installation that
-- asks nobody who they are has none.
alter table turns add column created_by binary(16),
    add constraint turns_created_by foreign key (created_by) references users (id);

-- +goose Down
alter table turns drop foreign key turns_created_by, drop column created_by;
