-- +goose Up
set local lock_timeout = '5s';
-- A session whose home was an archive has no home any more. Supervisors that
-- archive homes do not run on this schema.
update sessions set sandbox_state = case when sandbox is not null then 'awake' end
where sandbox_state in ('archived', 'restoring');
alter table sessions
    drop column asleep_since,
    drop column archive,
    drop column archive_bytes,
    drop column archived_at,
    drop column claim,
    drop column claimed_at;
update schema_floor set version = 9;

-- +goose Down
set local lock_timeout = '5s';
alter table sessions
    add column asleep_since timestamptz,
    add column archive uuid,
    add column archive_bytes bigint,
    add column archived_at timestamptz,
    add column claim uuid,
    add column claimed_at timestamptz;
update schema_floor set version = 0;
