-- +goose Up
set local lock_timeout = '5s';

-- Why a turn failed: a word for the kind of failure and what is known of it.
alter table turns
    add column failure_reason text,
    add column failure_message text;

-- +goose Down
set local lock_timeout = '5s';

alter table turns
    drop column failure_reason,
    drop column failure_message;
