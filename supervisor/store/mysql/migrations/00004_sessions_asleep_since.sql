-- +goose Up
-- A home is archived by the time since which its sandbox sleeps. A sandbox
-- that sleeps without that time has slept since now.
update sessions set asleep_since = now(6)
where sandbox is not null and sandbox_state = 'asleep' and asleep_since is null;

-- +goose Down
-- Nothing to undo: the times are like those that a Supervisor records.
