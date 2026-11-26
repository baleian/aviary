-- +goose Up
-- A session whose home was an archive has no home any more. Supervisors that
-- archive homes do not run on this schema.
update sessions set sandbox_state = if(sandbox is null, null, 'awake')
where sandbox_state in ('archived', 'restoring');
alter table sessions
    drop column asleep_since,
    drop column archive,
    drop column archive_bytes,
    drop column archived_at,
    drop column claim,
    drop column claimed_at,
    algorithm = instant;
update schema_floor set version = 9;

-- +goose Down
alter table sessions
    add column asleep_since datetime(6),
    add column archive binary(16),
    add column archive_bytes bigint,
    add column archived_at datetime(6),
    add column claim binary(16),
    add column claimed_at datetime(6),
    algorithm = instant;
update schema_floor set version = 0;
