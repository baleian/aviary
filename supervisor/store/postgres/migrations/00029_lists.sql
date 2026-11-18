-- +goose NO TRANSACTION
-- +goose Up
-- The agents of a project and the sessions of an agent are listed: the
-- sessions newest first, in the order of their IDs. The indexes are built
-- while the tables are in use, and one that a run that failed left behind
-- is built anew.
drop index concurrently if exists agents_project;
create index concurrently agents_project on agents (project_id);
drop index concurrently if exists sessions_agent;
create index concurrently sessions_agent on sessions (agent_id, id);

-- +goose Down
drop index concurrently if exists sessions_agent;
drop index concurrently if exists agents_project;
