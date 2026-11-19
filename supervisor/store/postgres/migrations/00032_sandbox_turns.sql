-- +goose NO TRANSACTION
-- +goose Up
-- A request that leaves a sandbox is told apart by the turn that runs in the
-- sandbox, which is found by the session whose sandbox it is. The index is
-- built while the table is in use, and one that a run that failed left
-- behind is built anew.
drop index concurrently if exists sessions_sandbox;
create index concurrently sessions_sandbox on sessions (sandbox) where sandbox is not null;

-- +goose Down
drop index concurrently if exists sessions_sandbox;
