-- +goose Up
-- When the turn was dispatched. A turn that still has no Job some time
-- after that has lost it.
alter table turns add column dispatched_at timestamptz;

-- +goose Down
alter table turns drop column dispatched_at;
