-- +goose Up
set local lock_timeout = '5s';

-- The token usage of the turn as its harness reported it. A harness need not
-- report it, and harnesses differ in what they count, so it is not a
-- measurement.
alter table turns
    add column total_tokens bigint,
    add column input_tokens bigint,
    add column output_tokens bigint,
    add column thought_tokens bigint,
    add column cached_read_tokens bigint,
    add column cached_write_tokens bigint;

-- +goose Down
set local lock_timeout = '5s';

alter table turns
    drop column total_tokens,
    drop column input_tokens,
    drop column output_tokens,
    drop column thought_tokens,
    drop column cached_read_tokens,
    drop column cached_write_tokens;
