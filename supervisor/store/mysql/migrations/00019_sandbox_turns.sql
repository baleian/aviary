-- +goose Up
-- A request that leaves a sandbox is told apart by the turn that runs in the
-- sandbox, which is found by the session whose sandbox it is. The index is
-- built while the table is in use.
create index sessions_sandbox on sessions (sandbox) algorithm = inplace lock = none;

-- +goose Down
drop index sessions_sandbox on sessions;
