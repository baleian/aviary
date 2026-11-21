-- name: CreateProject :one
insert into projects (id, slug, name) values (@id, @slug, @name) returning *;

-- name: GetProject :one
select * from projects where id = @id;

-- name: CreateAgent :one
insert into agents (id, project_id, slug, name, description, harness, instructions, approval, provider_id, model)
select @id::uuid, projects.id, @slug::text, @name::text, @description::text, @harness::text, @instructions::text,
       @approval::text, @provider_id::uuid, @model::text
from projects where projects.id = @project_id
returning *;

-- name: GetAgent :one
select * from agents where id = @id;

-- name: ListAgents :many
select * from agents where project_id = @project_id order by created_at, id;

-- name: CreateSession :one
insert into sessions (id, agent_id, title, created_by)
select @id::uuid, agents.id, @title::text, sqlc.narg(created_by)::uuid from agents where agents.id = @agent_id
returning *;

-- name: GetSession :one
select * from sessions where id = @id;

-- name: SetSessionTitle :one
update sessions set title = @title where id = @id returning *;

-- name: SetSandbox :execrows
-- Changes the session's sandbox only if it is still @was. An empty string
-- means none. A sandbox that is set is awake.
update sessions
set sandbox = nullif(sqlc.arg(sandbox)::text, ''),
    sandbox_state = case when sqlc.arg(sandbox)::text <> '' then 'awake' end
where id = @id and coalesce(sandbox, '') = sqlc.arg(was)::text;

-- name: SetSandboxState :exec
-- Changes the state only while the session has the sandbox.
update sessions set sandbox_state = sqlc.arg(state)::text where id = @id and sandbox = sqlc.arg(sandbox)::text;

-- name: ListSessionsWithSandbox :many
select * from sessions where sandbox is not null;

-- name: HoldSession :one
-- Keeps the session from being ended until the transaction ends. It takes
-- only the key share lock that adding the turn takes anyway: a stronger lock
-- would block updates of the session, which could deadlock.
select id from sessions where id = @id for key share;

-- name: LockSession :one
-- Locks the session until the transaction ends, so that no message is
-- accepted and no other transaction ends it meanwhile.
select * from sessions where id = @id for update;

-- name: EndSession :one
-- Ends the session unless it has an unfinished turn.
update sessions set ended_at = now()
where sessions.id = @id
  and not exists (select 1 from turns where turns.session_id = sessions.id and turns.ended_at is null)
returning *;

-- name: ListSessions :many
-- Lists the agent's sessions with IDs below @before, newest first: IDs
-- increase with creation time.
select * from sessions where agent_id = @agent_id and id < @before order by id desc limit @count;

-- name: ListSessionsOf :many
-- Like ListSessions, for the sessions that one user created.
select * from sessions where agent_id = @agent_id and created_by = @created_by::uuid and id < @before
order by id desc limit @count;

-- name: CreateProvider :one
insert into providers (id, project_id, name, url, api_key, api_key_store_id, api_key_path, api_key_name)
select @id::uuid, projects.id, @name::text, @url::text, sqlc.narg(api_key)::bytea,
       sqlc.narg(api_key_store_id)::uuid, @api_key_path::text, @api_key_name::text
from projects where projects.id = @project_id
returning *;

-- name: GetProvider :one
select * from providers where id = @id;

-- name: ListProviders :many
select * from providers where project_id = @project_id order by created_at, id;

-- name: ReplaceProvider :one
update providers set name = @name, url = @url, api_key = sqlc.narg(api_key),
       api_key_store_id = sqlc.narg(api_key_store_id), api_key_path = @api_key_path, api_key_name = @api_key_name,
       updated_at = now()
where id = @id
returning *;

-- name: CreateSecretStore :one
insert into secret_stores (id, project_id, name, kind, settings, credential)
select @id::uuid, projects.id, @name::text, @kind::text, @settings::jsonb, @credential::bytea
from projects where projects.id = @project_id
returning *;

-- name: GetSecretStore :one
select * from secret_stores where id = @id;

-- name: ListSecretStores :many
select * from secret_stores where project_id = @project_id order by created_at, id;

-- name: ReplaceSecretStore :one
update secret_stores
set name = @name, kind = @kind, settings = @settings, credential = @credential, updated_at = now()
where id = @id
returning *;

-- name: StartTurn :one
-- Records that the harness has received the message, and returns the seconds
-- since dispatch. It returns no row if the turn had already started.
update turns set started_at = now()
where id = @turn_id and started_at is null
returning coalesce(extract(epoch from now() - dispatched_at), 0)::float8 as startup;

-- name: Lock :exec
-- Waits for the advisory lock with the key and holds it until the
-- transaction ends.
select pg_advisory_xact_lock(@key);

-- name: CreateTurn :one
-- Returns no row if the session does not exist or has ended.
insert into turns (id, session_id, seq, state, text, created_by)
select @id::uuid, sessions.id,
       (select coalesce(max(seq), 0) + 1 from turns where session_id = sessions.id),
       'queued', @text::text, sqlc.narg(created_by)::uuid
from sessions where sessions.id = @session_id and sessions.ended_at is null
returning *;

-- name: GetTurn :one
select * from turns where id = @id;

-- name: ListTurns :many
select * from turns where session_id = @session_id and seq < @before order by seq desc limit @count;

-- name: GetTurnHarness :one
select agents.harness
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
where turns.id = @id;

-- name: CountWaitingTurns :one
-- Counts the queued turns that have no room to run among @max_running. The
-- count is negative if room is left.
select ((select count(*) from turns where state = 'queued')
        - greatest(0, sqlc.arg(max_running)::bigint
                      - (select count(*) from turns where state in ('dispatching', 'running'))))::bigint as waiting;

-- name: QueuedTurns :many
select id from turns where state = 'queued' order by created_at, id;

-- name: CancellingTurns :many
select id from turns where ended_at is null and cancel_requested_at is not null;

-- name: NextTurns :many
-- Lists the oldest queued turns that have room to run among @max_running.
select turns.id as turn_id, agents.harness, extract(epoch from now() - turns.created_at)::float8 as waited
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
where turns.state = 'queued'
order by turns.created_at, turns.id
limit greatest(sqlc.arg(max_running)::bigint
               - (select count(*) from turns as running where running.state in ('dispatching', 'running')), 0);

-- name: DispatchTurn :execrows
-- Dispatches the turn if it is still queued: it may have been cancelled since
-- it was read.
update turns set state = 'dispatching', dispatched_at = now()
where id = @id and state = 'queued';

-- name: DispatchedTurns :many
select id, coalesce(extract(epoch from now() - cancel_requested_at), 0)::float8 as cancelling
from turns where state in ('dispatching', 'running');

-- name: FollowTurn :execrows
update turns set state = 'running' where id = @id and state in ('dispatching', 'running');

-- name: FollowedTurn :one
select sqlc.embed(turns), agents.id as agent_id, agents.project_id, agents.harness, agents.instructions,
       agents.approval, providers.url as provider_url, agents.model, sessions.sandbox,
       extract(epoch from now() - coalesce(turns.dispatched_at, turns.created_at))::float8 as dispatched
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
join providers on providers.id = agents.provider_id
where turns.id = @id;

-- name: SessionTurn :one
-- Returns the origin of the session's running turn, with the current
-- provider of its agent and the secret store of the provider's key.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id, turns.created_by,
       coalesce(users.subject, '')::text as subject,
       agents.provider_id, providers.url as provider_url, providers.api_key as provider_api_key,
       providers.api_key_store_id, providers.api_key_path, providers.api_key_name,
       coalesce(secret_stores.name, '')::text as store_name, coalesce(secret_stores.kind, '')::text as store_kind,
       secret_stores.settings as store_settings, secret_stores.credential as store_credential,
       secret_stores.created_at as store_created_at, secret_stores.updated_at as store_updated_at
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
join providers on providers.id = agents.provider_id
left join users on users.id = turns.created_by
left join secret_stores on secret_stores.id = providers.api_key_store_id
where turns.session_id = @session_id and turns.state = 'running';

-- name: SetFinished :exec
update turns set stop_reason = @stop_reason::text, total_tokens = @total_tokens, input_tokens = @input_tokens,
       output_tokens = @output_tokens, thought_tokens = @thought_tokens,
       cached_read_tokens = @cached_read_tokens, cached_write_tokens = @cached_write_tokens
where id = @id;

-- name: CancelTurn :one
-- Ends a queued turn at once, and only requests cancellation of a dispatched
-- one.
update turns set
       state = case when state = 'queued' then 'cancelled' else state end,
       ended_at = case when state = 'queued' then now() end,
       cancel_requested_at = coalesce(cancel_requested_at, now())
where id = @id and ended_at is null
returning *;

-- name: ExpireTurns :many
update turns set state = 'expired', ended_at = now()
where state = 'queued' and created_at < now() - make_interval(secs => @seconds::float8)
returning *;

-- name: EndTurn :one
-- Ends the turn as cancelled, completed or failed, as store.Store.EndTurn
-- describes.
with ending as (
    select case
               when cancel_requested_at is not null
                    and not (@succeeded::boolean and coalesce(stop_reason, 'cancelled') <> 'cancelled') then 'cancelled'
               when @succeeded::boolean and stop_reason is not null then 'completed'
               else 'failed'
           end as state
    from turns where id = @id and ended_at is null
)
update turns set ended_at = now(), state = ending.state,
       failure_reason = case when ending.state = 'failed' then @reason::text end,
       failure_message = case when ending.state = 'failed' then nullif(@message::text, '') end
from ending
where turns.id = @id and turns.ended_at is null
returning turns.*;

-- name: AddTurnItems :copyfrom
insert into turn_items (turn_id, seq, item) values (@turn_id, @seq, @item);

-- name: ListTurnItems :many
select turn_id, item from turn_items where turn_id = any(@turn_ids::uuid[]) order by turn_id, seq;

-- name: AddApproval :exec
-- Does nothing if the request exists, since a runner may report a request
-- again.
insert into approvals (turn_id, id, request) values (@turn_id, @id, @request)
on conflict (turn_id, id) do nothing;

-- name: ResolveApproval :one
-- Records that the harness has received its answer, and returns the request
-- and the seconds it waited. It returns no row if the request was already
-- resolved.
update approvals set outcome = @outcome, resolved_at = now()
where turn_id = @turn_id and id = @id and resolved_at is null
returning request, extract(epoch from now() - requested_at)::float8 as waited;

-- name: GetApproval :one
select * from approvals where turn_id = @turn_id and id = @id;

-- name: AnswerApproval :execrows
-- Records the chosen option, unless the request has an answer already or is
-- resolved, or its turn has ended.
update approvals set option_id = @option_id::text, answered_at = now(), answered_by = sqlc.narg(answered_by)
from turns
where approvals.turn_id = @turn_id and approvals.id = @id
  and approvals.answered_at is null and approvals.resolved_at is null
  and turns.id = approvals.turn_id and turns.ended_at is null;

-- name: WaitingApprovals :many
select request from approvals
where turn_id = @turn_id and answered_at is null and resolved_at is null
order by requested_at, id;

-- name: Answers :many
-- Lists the answers not yet delivered to the harnesses of unfinished turns:
-- those of the turn, or of all turns if turn_id is null.
select approvals.turn_id, approvals.id, approvals.option_id::text as option_id
from approvals
join turns on turns.id = approvals.turn_id
where approvals.answered_at is not null and approvals.resolved_at is null and turns.ended_at is null
  and (sqlc.narg(turn_id)::uuid is null or approvals.turn_id = sqlc.narg(turn_id));

-- name: GetUserBySubject :one
select * from users where subject = @subject;

-- name: FreeUsername :exec
-- Takes the username from any other user, since the provider has given it to
-- the user with the subject.
update users set username = null where username = @username::text and subject <> @subject::text;

-- name: SeeUser :one
-- Creates the user with the subject, or updates it if it exists. A null
-- username keeps the current one.
insert into users (id, subject, username, display_name)
values (@id, @subject, sqlc.narg(username), @display_name)
on conflict (subject) do update
set username = coalesce(excluded.username, users.username), display_name = excluded.display_name
returning *;

-- name: ListRoles :many
select * from roles order by name;

-- name: SetRole :one
update roles set permissions = @permissions, updated_at = now(), updated_by = sqlc.narg(updated_by)
where name = @name
returning *;

-- name: IsMember :one
-- Reports whether the user who has the username is a member of the project.
select exists (
    select 1 from project_members
    join users on users.id = project_members.user_id
    where project_members.project_id = @project_id and users.username = @username::text
);

-- name: AddMember :one
insert into project_members (id, project_id, username, group_name, role, permissions, updated_by)
select @id::uuid, projects.id, sqlc.narg(username)::text, sqlc.narg(group_name)::text,
       sqlc.narg(role)::text, sqlc.narg(permissions)::text[], sqlc.narg(updated_by)::uuid
from projects where projects.id = @project_id
returning id;

-- name: DropMembersBeforeClaim :exec
-- Deletes the membership of the user who has the username in each project
-- that awaits a member under it, so that the awaited member takes its place.
delete from project_members as member
using project_members as awaited, users
where awaited.project_id = member.project_id and awaited.username = @username::text
  and users.id = member.user_id and users.username = @username::text;

-- name: ClaimMembers :exec
-- Gives the members awaited under the username to the user who has it.
update project_members set user_id = users.id, username = null
from users
where project_members.username = @username::text and users.username = @username::text;

-- name: SetMember :execrows
update project_members
set role = sqlc.narg(role), permissions = sqlc.narg(permissions)::text[], updated_at = now(), updated_by = sqlc.narg(updated_by)
where id = @id;

-- name: RemoveMember :execrows
delete from project_members where id = @id;

-- name: GetMember :one
select project_members.*, users.subject, users.username as user_username, users.display_name, users.created_at as user_created_at
from project_members left join users on users.id = project_members.user_id
where project_members.id = @id;

-- name: ListMembers :many
select project_members.*, users.subject, users.username as user_username, users.display_name, users.created_at as user_created_at
from project_members left join users on users.id = project_members.user_id
where project_members.project_id = @project_id
order by project_members.created_at, project_members.id;

-- name: ListProjects :many
select * from projects order by created_at, id;

-- name: ListProjectsOfMember :many
select projects.* from projects
where exists (
    select 1 from project_members
    where project_members.project_id = projects.id
      and (project_members.user_id = sqlc.narg(user_id)::uuid or project_members.group_name = any(@groups::text[]))
)
order by projects.created_at, projects.id;

-- name: ListPermissions :many
-- Returns, for each member of the project that is the user or one of the
-- groups, its own permissions or those of its role.
select coalesce(project_members.permissions, roles.permissions)::text[] as permissions
from project_members left join roles on roles.name = project_members.role
where project_members.project_id = @project_id
  and (project_members.user_id = sqlc.narg(user_id)::uuid or project_members.group_name = any(@groups::text[]));
