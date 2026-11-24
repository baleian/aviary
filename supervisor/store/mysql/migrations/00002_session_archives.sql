-- +goose Up
-- The home of a session can be an archive in the depot. sandbox_state is then
-- 'archived' while the session has no sandbox, and 'restoring' while it has
-- one for that home.
alter table sessions
    -- When the sandbox was put to sleep, while it is asleep.
    add column asleep_since datetime(6),
    -- The archive of the session's home, its size and when it was made, while
    -- the home is archived or being restored.
    add column archive binary(16),
    add column archive_bytes bigint,
    add column archived_at datetime(6),
    -- The claim of the Supervisor that archives or restores the home, and
    -- when it was made. The time stays when the claim is gone.
    add column claim binary(16),
    add column claimed_at datetime(6),
    algorithm = instant;

-- +goose Down
-- A session whose home was an archive has no home any more.
update sessions set sandbox_state = if(sandbox is null, null, 'awake')
where sandbox_state in ('archived', 'restoring');
alter table sessions
    drop column asleep_since,
    drop column archive,
    drop column archive_bytes,
    drop column archived_at,
    drop column claim,
    drop column claimed_at;
