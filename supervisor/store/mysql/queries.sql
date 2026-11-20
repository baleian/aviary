-- name: CreateProject :exec
insert into projects (id, name) values (sqlc.arg(id), sqlc.arg(name));

-- name: GetProject :one
select * from projects where id = ?;

-- name: CreateAgent :execrows
insert into agents (id, project_id, harness, instructions, approval, provider_id, model)
select sqlc.arg(id), projects.id, sqlc.arg(harness), sqlc.arg(instructions), sqlc.arg(approval),
       sqlc.narg(provider_id), sqlc.arg(model)
from projects where projects.id = sqlc.arg(project_id);

-- name: GetAgent :one
select * from agents where id = ?;

-- name: ListAgents :many
select * from agents where project_id = ? order by created_at, id;

-- name: CreateSession :execrows
insert into sessions (id, agent_id, created_by)
select sqlc.arg(id), agents.id, sqlc.narg(created_by) from agents where agents.id = sqlc.arg(agent_id);

-- name: GetSession :one
select * from sessions where id = ?;

-- name: SetSandbox :exec
update sessions set sandbox = sqlc.narg(sandbox) where id = sqlc.arg(id);

-- name: ListSessions :many
-- Lists the agent's sessions with IDs below before, newest first: IDs
-- increase with creation time.
select * from sessions where agent_id = sqlc.arg(agent_id) and id < sqlc.arg(before) order by id desc limit ?;

-- name: ListSessionsOf :many
-- Like ListSessions, for the sessions that one user created.
select * from sessions
where agent_id = sqlc.arg(agent_id) and created_by = sqlc.arg(created_by) and id < sqlc.arg(before)
order by id desc limit ?;

-- name: LockSession :one
-- Locks the session until the transaction ends, which serializes accepting
-- its messages and ending it.
select * from sessions where id = ? for update;

-- name: IsSessionBusy :one
-- Reports whether the session has an unfinished turn.
select exists (select 1 from turns where session_id = ? and ended_at is null);

-- name: EndSession :exec
update sessions set ended_at = now(6) where id = ?;

-- name: CreateProvider :execrows
insert into providers (id, project_id, name, url, api_key, api_key_store_id, api_key_path, api_key_name)
select sqlc.arg(id), projects.id, sqlc.arg(name), sqlc.arg(url), sqlc.narg(api_key), sqlc.narg(api_key_store_id),
       sqlc.arg(api_key_path), sqlc.arg(api_key_name)
from projects where projects.id = sqlc.arg(project_id);

-- name: GetProvider :one
select * from providers where id = ?;

-- name: ReplaceProvider :execrows
update providers set name = sqlc.arg(name), url = sqlc.arg(url), api_key = sqlc.narg(api_key),
       api_key_store_id = sqlc.narg(api_key_store_id), api_key_path = sqlc.arg(api_key_path),
       api_key_name = sqlc.arg(api_key_name), updated_at = now(6)
where id = sqlc.arg(id);

-- name: CreateSecretStore :execrows
insert into secret_stores (id, project_id, name, kind, settings, credential)
select sqlc.arg(id), projects.id, sqlc.arg(name), sqlc.arg(kind), sqlc.arg(settings), sqlc.arg(credential)
from projects where projects.id = sqlc.arg(project_id);

-- name: GetSecretStore :one
select * from secret_stores where id = ?;

-- name: ReplaceSecretStore :execrows
update secret_stores
set name = sqlc.arg(name), kind = sqlc.arg(kind), settings = sqlc.arg(settings), credential = sqlc.arg(credential),
    updated_at = now(6)
where id = sqlc.arg(id);

-- name: Lock :one
-- Waits for the lock with the name and holds it until the transaction ends.
select name from locks where name = ? for update;

-- name: NextSeq :one
select cast(coalesce(max(seq), 0) + 1 as signed) as seq from turns where session_id = ?;

-- name: CreateTurn :exec
insert into turns (id, session_id, seq, state, text, created_by)
values (sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(seq), 'queued', sqlc.arg(text), sqlc.narg(created_by));

-- name: GetTurn :one
select * from turns where id = ?;

-- name: LockTurn :one
-- Locks the turn until the transaction ends, which serializes changes to it.
select * from turns where id = ? for update;

-- name: ListTurns :many
select * from turns where session_id = sqlc.arg(session_id) and seq < sqlc.arg(before) order by seq desc limit ?;

-- name: GetTurnHarness :one
select agents.harness
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
where turns.id = ?;

-- name: CountQueuedTurns :one
select count(*) from turns where state = 'queued';

-- name: CountRunningTurns :one
-- Counts the dispatching and running turns, which both take room to run.
select count(*) from turns where state in ('dispatching', 'running');

-- name: QueuedTurns :many
select id from turns where state = 'queued' order by created_at, id;

-- name: CancellingTurns :many
select id from turns where ended_at is null and cancel_requested_at is not null;

-- name: NextTurns :many
select turns.id as turn_id, agents.harness, cast(timestampdiff(microsecond, turns.created_at, now(6)) as signed) as waited
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
where turns.state = 'queued'
order by turns.created_at, turns.id
limit ?;

-- name: DispatchTurn :execrows
-- Dispatches the turn if it is still queued: it may have been cancelled since
-- it was read.
update turns set state = 'dispatching', dispatched_at = now(6) where id = ? and state = 'queued';

-- name: DispatchedTurns :many
select id, cast(coalesce(timestampdiff(microsecond, cancel_requested_at, now(6)), 0) as signed) as cancelling
from turns where state in ('dispatching', 'running');

-- name: FollowTurn :exec
update turns set state = 'running' where id = ? and state in ('dispatching', 'running');

-- name: FollowedTurn :one
select sqlc.embed(turns), agents.id as agent_id, agents.project_id, agents.harness, agents.instructions,
       agents.approval, coalesce(providers.url, '') as provider_url, agents.model, sessions.sandbox,
       cast(timestampdiff(microsecond, coalesce(turns.dispatched_at, turns.created_at), now(6)) as signed) as dispatched
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join providers on providers.id = agents.provider_id
where turns.id = ?;

-- name: SessionTurn :one
-- Returns the origin of the session's running turn, with the current
-- provider of its agent and the secret store of the provider's key.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id, turns.created_by,
       coalesce(users.subject, '') as subject,
       agents.provider_id, coalesce(providers.url, '') as provider_url, providers.api_key as provider_api_key,
       providers.api_key_store_id, coalesce(providers.api_key_path, '') as api_key_path,
       coalesce(providers.api_key_name, '') as api_key_name,
       coalesce(secret_stores.name, '') as store_name, coalesce(secret_stores.kind, '') as store_kind,
       secret_stores.settings as store_settings, secret_stores.credential as store_credential,
       secret_stores.created_at as store_created_at, secret_stores.updated_at as store_updated_at
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join users on users.id = turns.created_by
left join providers on providers.id = agents.provider_id
left join secret_stores on secret_stores.id = providers.api_key_store_id
where turns.session_id = ? and turns.state = 'running';

-- name: StartTurn :exec
update turns set started_at = now(6) where id = ?;

-- name: Startup :one
-- Returns the microseconds from the turn's dispatch to its start.
select cast(coalesce(timestampdiff(microsecond, dispatched_at, started_at), 0) as signed) as startup from turns where id = ?;

-- name: SetFinished :exec
update turns set stop_reason = sqlc.arg(stop_reason), total_tokens = sqlc.narg(total_tokens),
       input_tokens = sqlc.narg(input_tokens), output_tokens = sqlc.narg(output_tokens),
       thought_tokens = sqlc.narg(thought_tokens), cached_read_tokens = sqlc.narg(cached_read_tokens),
       cached_write_tokens = sqlc.narg(cached_write_tokens)
where id = sqlc.arg(id);

-- name: CancelQueuedTurn :exec
update turns set state = 'cancelled', ended_at = now(6), cancel_requested_at = coalesce(cancel_requested_at, now(6))
where id = ?;

-- name: AskTurnToStop :exec
update turns set cancel_requested_at = coalesce(cancel_requested_at, now(6)) where id = ?;

-- name: LockTurnsToExpire :many
select id from turns
where state = 'queued' and created_at < now(6) - interval sqlc.arg(microseconds) microsecond
order by created_at, id
for update;

-- name: ExpireTurn :exec
update turns set state = 'expired', ended_at = now(6) where id = ?;

-- name: EndTurn :exec
update turns set ended_at = now(6), state = sqlc.arg(state), failure_reason = sqlc.narg(failure_reason),
       failure_message = nullif(sqlc.narg(failure_message), '')
where id = sqlc.arg(id);

-- name: AddTurnItem :exec
insert into turn_items (turn_id, seq, item) values (?, ?, ?);

-- name: ListTurnItems :many
select turn_id, item from turn_items where turn_id in (sqlc.slice('turn_ids')) order by turn_id, seq;

-- name: AddApproval :exec
-- Does nothing if the request exists, since a runner may report a request
-- again.
insert into approvals (turn_id, id, request) values (?, ?, ?)
on duplicate key update turn_id = turn_id;

-- name: GetApproval :one
select * from approvals where turn_id = ? and id = ?;

-- name: ResolveApproval :execrows
-- Records that the harness has received its answer, unless the request was
-- already resolved.
update approvals set outcome = sqlc.arg(outcome), resolved_at = now(6)
where turn_id = sqlc.arg(turn_id) and id = sqlc.arg(id) and resolved_at is null;

-- name: AnswerApproval :execrows
-- Records the chosen option, unless the request has an answer already or is
-- resolved, or its turn has ended.
update approvals set option_id = sqlc.arg(option_id), answered_at = now(6), answered_by = sqlc.narg(answered_by)
where approvals.turn_id = sqlc.arg(turn_id) and approvals.id = sqlc.arg(id)
  and approvals.answered_at is null and approvals.resolved_at is null
  and exists (select 1 from turns where turns.id = approvals.turn_id and turns.ended_at is null);

-- name: WaitingApprovals :many
select request from approvals
where turn_id = ? and answered_at is null and resolved_at is null
order by requested_at, id;

-- name: Answers :many
-- Lists the answers not yet delivered to the harnesses of unfinished turns:
-- those of the turn, or of all turns if turn_id is null.
select approvals.turn_id, approvals.id, coalesce(approvals.option_id, '') as option_id
from approvals
join turns on turns.id = approvals.turn_id
where approvals.answered_at is not null and approvals.resolved_at is null and turns.ended_at is null
  and (sqlc.narg(turn_id) is null or approvals.turn_id = sqlc.narg(turn_id));

-- name: GetUserBySubject :one
select * from users where subject = ?;

-- name: FreeUsername :exec
-- Takes the username from any other user, since the provider has given it to
-- the user with the subject.
update users set username = null where username = sqlc.arg(username) and subject <> sqlc.arg(subject);

-- name: CreateUser :exec
insert into users (id, subject, username, display_name) values (?, ?, ?, ?);

-- name: UpdateUser :exec
-- Updates the user with the subject. A null username keeps the current one.
update users set username = coalesce(sqlc.narg(username), username), display_name = sqlc.arg(display_name)
where subject = sqlc.arg(subject);

-- name: ListRoles :many
select * from roles order by name;

-- name: GetRole :one
select * from roles where name = ?;

-- name: SetRole :exec
update roles set permissions = sqlc.arg(permissions), updated_at = now(6), updated_by = sqlc.narg(updated_by)
where name = sqlc.arg(name);

-- name: IsMember :one
-- Reports whether the user who has the username is a member of the project.
select exists (
    select 1 from project_members
    join users on users.id = project_members.user_id
    where project_members.project_id = sqlc.arg(project_id) and users.username = sqlc.arg(username)
);

-- name: AddMember :execrows
insert into project_members (id, project_id, username, group_name, role, permissions, updated_by)
select sqlc.arg(id), projects.id, sqlc.narg(username), sqlc.narg(group_name),
       sqlc.narg(role), sqlc.narg(permissions), sqlc.narg(updated_by)
from projects where projects.id = sqlc.arg(project_id);

-- name: DropMembersBeforeClaim :exec
-- Deletes the membership of the user who has the username in each project
-- that awaits a member under it, so that the awaited member takes its place.
delete member from project_members as member
join project_members as awaited on awaited.project_id = member.project_id and awaited.username = sqlc.arg(username)
join users on users.id = member.user_id and users.username = sqlc.arg(username);

-- name: ClaimMembers :exec
-- Gives the members awaited under the username to the user who has it.
update project_members
join users on users.username = project_members.username
set project_members.user_id = users.id, project_members.username = null
where project_members.username = sqlc.arg(username);

-- name: SetMember :exec
update project_members
set role = sqlc.narg(role), permissions = sqlc.narg(permissions), updated_at = now(6), updated_by = sqlc.narg(updated_by)
where id = sqlc.arg(id);

-- name: RemoveMember :execrows
delete from project_members where id = ?;

-- name: GetMember :one
select project_members.*, users.subject, users.username as user_username, users.display_name, users.created_at as user_created_at
from project_members left join users on users.id = project_members.user_id
where project_members.id = ?;

-- name: ListMembers :many
select project_members.*, users.subject, users.username as user_username, users.display_name, users.created_at as user_created_at
from project_members left join users on users.id = project_members.user_id
where project_members.project_id = ?
order by project_members.created_at, project_members.id;

-- name: ListProjects :many
select * from projects order by created_at, id;

-- name: ListProjectsOfMember :many
select projects.* from projects
where exists (
    select 1 from project_members
    where project_members.project_id = projects.id
      and (project_members.user_id = sqlc.narg(user_id) or project_members.group_name in (sqlc.slice(group_names)))
)
order by projects.created_at, projects.id;

-- name: ListPermissions :many
-- Returns, for each member of the project that is the user or one of the
-- groups, its own permissions or those of its role.
select cast(coalesce(project_members.permissions, roles.permissions) as json) as permissions
from project_members left join roles on roles.name = project_members.role
where project_members.project_id = sqlc.arg(project_id)
  and (project_members.user_id = sqlc.narg(user_id) or project_members.group_name in (sqlc.slice(group_names)));
