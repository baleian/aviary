-- +goose Up
set local lock_timeout = '5s';
-- The home of a session can be an archive in the depot. sandbox_state is then
-- 'archived' while the session has no sandbox, and 'restoring' while it has
-- one for that home.
alter table sessions
    -- When the sandbox was put to sleep, while it is asleep.
    add column asleep_since timestamptz,
    -- The archive of the session's home, its size and when it was made, while
    -- the home is archived or being restored.
    add column archive uuid,
    add column archive_bytes bigint,
    add column archived_at timestamptz,
    -- The claim of the Supervisor that archives or restores the home, and
    -- when it was made. The time stays when the claim is gone.
    add column claim uuid,
    add column claimed_at timestamptz;

-- +goose Down
set local lock_timeout = '5s';
-- A session whose home was an archive has no home any more.
update sessions set sandbox_state = case when sandbox is not null then 'awake' end
where sandbox_state in ('archived', 'restoring');
alter table sessions
    drop column asleep_since,
    drop column archive,
    drop column archive_bytes,
    drop column archived_at,
    drop column claim,
    drop column claimed_at;
