-- name: CreateProject :one
insert into projects (id, name) values (@id, @name) returning *;

-- name: GetProject :one
select * from projects where id = @id;

-- name: CreateAgent :one
insert into agents (id, project_id, harness, instructions, resources, approval, provider_id, model)
select @id::uuid, projects.id, @harness::text, @instructions::text, @resources::jsonb, @approval::text,
       sqlc.narg(provider_id)::uuid, @model::text
from projects where projects.id = @project_id
returning *;

-- name: GetAgent :one
select * from agents where id = @id;

-- name: CreateSession :one
insert into sessions (id, agent_id, created_by)
select @id::uuid, agents.id, sqlc.narg(created_by)::uuid from agents where agents.id = @agent_id
returning *;

-- name: GetSession :one
select * from sessions where id = @id;

-- name: CreateProvider :one
insert into providers (id, project_id, name, url, api_key, api_key_store_id, api_key_path, api_key_name)
select @id::uuid, projects.id, @name::text, @url::text, sqlc.narg(api_key)::bytea,
       sqlc.narg(api_key_store_id)::uuid, @api_key_path::text, @api_key_name::text
from projects where projects.id = @project_id
returning *;

-- name: GetProvider :one
select * from providers where id = @id;

-- name: ReplaceProvider :one
-- The provider gets the name, the URL and the key that are given, in place
-- of all that it had. The key is stored, or in a secret store, or none.
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

-- name: ReplaceSecretStore :one
-- The store gets the name, the kind, the settings and the credential that
-- are given, in place of all that it had.
update secret_stores
set name = @name, kind = @kind, settings = @settings, credential = @credential, updated_at = now()
where id = @id
returning *;

-- name: StartTurn :one
-- The harness of the turn has been given the message. The session keeps the
-- harness session, for its next turn to continue. The answer is how long the
-- turn took to start. There is none for a turn that had started before.
with started as (
    update turns set started_at = now() where turns.id = @turn_id and turns.started_at is null
    returning turns.session_id, coalesce(extract(epoch from now() - turns.dispatched_at), 0)::float8 as startup
)
update sessions set harness_session_id = @harness_session_id::text
from started
where sessions.id = started.session_id
returning started.startup;

-- name: Lock :exec
-- Waits for the advisory lock of the key and holds it until the transaction
-- ends.
select pg_advisory_xact_lock(@key);

-- name: CreateTurn :one
insert into turns (id, session_id, seq, state, text, created_by)
select @id::uuid, sessions.id,
       (select coalesce(max(seq), 0) + 1 from turns where session_id = sessions.id),
       'queued', @text::text, sqlc.narg(created_by)::uuid
from sessions where sessions.id = @session_id
returning *;

-- name: GetTurn :one
select * from turns where id = @id;

-- name: ListTurns :many
-- The turns of a session that came before the one with the sequence number
-- @before, newest first.
select * from turns where session_id = @session_id and seq < @before order by seq desc limit @count;

-- name: GetTurnHarness :one
select agents.harness
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
where turns.id = @id;

-- name: CountWaitingTurns :one
-- A queued turn waits only if the turns that run leave it no room.
select ((select count(*) from turns where state = 'queued')
        - greatest(0, sqlc.arg(max_running)::bigint
                      - (select count(*) from turns where state in ('dispatching', 'running'))))::bigint as waiting;

-- name: QueuedTurns :many
select id from turns where state = 'queued' order by created_at, id;

-- name: CancellingTurns :many
select id from turns where ended_at is null and cancel_requested_at is not null;

-- name: NextTurns :many
-- The queued turns that there is room to run, oldest first. A turn of an
-- agent that names its model has the URL of the agent's provider and the
-- name of the model.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id, agents.harness,
       agents.resources, agents.approval, coalesce(providers.url, '')::text as provider_url, agents.model,
       extract(epoch from now() - turns.created_at)::float8 as waited
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join providers on providers.id = agents.provider_id
where turns.state = 'queued'
order by turns.created_at, turns.id
limit greatest(sqlc.arg(max_running)::bigint - (select count(*) from turns where state in ('dispatching', 'running')), 0);

-- name: DispatchTurn :execrows
-- A turn that was cancelled since it was read is left as it is.
update turns set state = 'dispatching', token_hash = @token_hash, dispatched_at = now()
where id = @id and state = 'queued';

-- name: DispatchedTurns :many
select id,
       (state = 'running')::boolean as taken,
       extract(epoch from now() - coalesce(dispatched_at, created_at))::float8 as age,
       coalesce(extract(epoch from now() - cancel_requested_at), 0)::float8 as cancelling
from turns where state in ('dispatching', 'running');

-- name: TakeTurn :one
-- A turn that waits for its runner is given to the runner that asks. A turn
-- that runs is given again to the runner that took it.
with asked as (
    select turns.id, turns.session_id, turns.state from turns where turns.token_hash = @token_hash for update
)
update turns set state = 'running', runner_id = sqlc.narg(runner_id)
from asked
join sessions on sessions.id = asked.session_id
join agents on agents.id = sessions.agent_id
where turns.id = asked.id
      and ((turns.state = 'dispatching' and turns.cancel_requested_at is null and turns.failure_reason is null)
           or (turns.state = 'running' and turns.runner_id = sqlc.narg(runner_id)))
returning turns.id, turns.session_id, turns.seq, turns.text, turns.created_at, sessions.harness_session_id,
          agents.harness, agents.instructions, (asked.state = 'running')::boolean as again;

-- name: GetTurnState :one
select state from turns where token_hash = @token_hash;

-- name: RunningTurn :one
-- The running turn that the token belongs to, what the turn belongs to, and
-- the provider of its agent as it is now, if the agent names its model. A
-- provider whose key is in a secret store comes with that store, as it is
-- now.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id,
       agents.provider_id, coalesce(providers.url, '')::text as provider_url, providers.api_key as provider_api_key,
       providers.api_key_store_id, coalesce(providers.api_key_path, '')::text as api_key_path,
       coalesce(providers.api_key_name, '')::text as api_key_name,
       coalesce(secret_stores.name, '')::text as store_name, coalesce(secret_stores.kind, '')::text as store_kind,
       secret_stores.settings as store_settings, secret_stores.credential as store_credential,
       secret_stores.created_at as store_created_at, secret_stores.updated_at as store_updated_at
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join providers on providers.id = agents.provider_id
left join secret_stores on secret_stores.id = providers.api_key_store_id
where turns.token_hash = @token_hash and turns.state = 'running';

-- name: SetFinished :exec
update turns set stop_reason = @stop_reason::text, total_tokens = @total_tokens, input_tokens = @input_tokens,
       output_tokens = @output_tokens, thought_tokens = @thought_tokens,
       cached_read_tokens = @cached_read_tokens, cached_write_tokens = @cached_write_tokens
where id = @id;

-- name: CancelTurn :one
-- A queued turn ends at once. A dispatched one is only marked.
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

-- name: GiveUpTurn :execrows
update turns set failure_reason = @reason::text, failure_message = nullif(@message::text, '')
where id = @id and state = 'dispatching' and failure_reason is null and cancel_requested_at is null;

-- name: EndTurn :one
-- A turn that was asked to stop is cancelled, unless the harness had ended
-- it by itself. Otherwise it is completed if its pod succeeded and the runner
-- reported why the harness stopped, and failed if not. A failed turn keeps
-- the reason that it was given up for, if it was.
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
       failure_reason = case when ending.state = 'failed' then coalesce(turns.failure_reason, @reason::text) end,
       failure_message = case
           when ending.state <> 'failed' then null
           when turns.failure_reason is not null then turns.failure_message
           else nullif(@message::text, '')
       end
from ending
where turns.id = @id and turns.ended_at is null
returning turns.*;

-- name: AddTurnItems :copyfrom
insert into turn_items (turn_id, seq, item) values (@turn_id, @seq, @item);

-- name: ListTurnItems :many
select turn_id, item from turn_items where turn_id = any(@turn_ids::uuid[]) order by turn_id, seq;

-- name: AddApproval :exec
-- A request that its runner reports again is there already.
insert into approvals (turn_id, id, request) values (@turn_id, @id, @request)
on conflict (turn_id, id) do nothing;

-- name: ResolveApproval :one
-- The harness has its answer. The answer here is the request and how long it
-- waited. There is none for a request that was resolved before.
update approvals set outcome = @outcome, resolved_at = now()
where turn_id = @turn_id and id = @id and resolved_at is null
returning request, extract(epoch from now() - requested_at)::float8 as waited;

-- name: GetApproval :one
select * from approvals where turn_id = @turn_id and id = @id;

-- name: AnswerApproval :execrows
-- Keeps the option that a person chose, for a request that has no answer
-- yet and whose turn has not ended.
update approvals set option_id = @option_id::text, answered_at = now()
from turns
where approvals.turn_id = @turn_id and approvals.id = @id
  and approvals.answered_at is null and approvals.resolved_at is null
  and turns.id = approvals.turn_id and turns.ended_at is null;

-- name: WaitingApprovals :many
-- The requests of a turn that wait for a person, oldest first.
select request from approvals
where turn_id = @turn_id and answered_at is null and resolved_at is null
order by requested_at, id;

-- name: Answers :many
-- The answers that the harnesses of turns that run have not got yet: those
-- of one turn, or of all.
select approvals.turn_id, approvals.id, approvals.option_id::text as option_id
from approvals
join turns on turns.id = approvals.turn_id
where approvals.answered_at is not null and approvals.resolved_at is null and turns.ended_at is null
  and (sqlc.narg(turn_id)::uuid is null or approvals.turn_id = sqlc.narg(turn_id));

-- name: GetUserBySubject :one
select * from users where subject = @subject;

-- name: FreeUsername :exec
-- The username is that of the user whom the provider gives it to now.
update users set username = null where username = @username::text and subject <> @subject::text;

-- name: SeeUser :one
-- Supervisors may see a user for the first time at once: one of them makes
-- the user. A user that is given no username keeps the one that it has.
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
-- Whether the user with the username is a member of the project.
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
-- A user who is a member of a project, and is given a username under which
-- the project was given a member since, is the member that was given last.
delete from project_members as member
using project_members as awaited, users
where awaited.project_id = member.project_id and awaited.username = @username::text
  and users.id = member.user_id and users.username = @username::text;

-- name: ClaimMembers :exec
-- The members that were awaited under the username are the user that has it.
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
-- The projects of which the user, or one of the groups, is a member.
select projects.* from projects
where exists (
    select 1 from project_members
    where project_members.project_id = projects.id
      and (project_members.user_id = sqlc.narg(user_id)::uuid or project_members.group_name = any(@groups::text[]))
)
order by projects.created_at, projects.id;
