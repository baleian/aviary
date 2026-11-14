-- +goose Up
set local lock_timeout = '5s';

-- When a turn that had been dispatched was asked to stop. Such a turn ends
-- as cancelled once its pod has stopped.
alter table turns add column cancel_requested_at timestamptz;

-- +goose Down
set local lock_timeout = '5s';

alter table turns drop column cancel_requested_at;
