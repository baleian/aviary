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
insert into sessions (id, agent_id)
select @id::uuid, agents.id from agents where agents.id = @agent_id
returning *;

-- name: GetSession :one
select * from sessions where id = @id;

-- name: CreateProvider :one
insert into providers (id, project_id, name, url, api_key)
select @id::uuid, projects.id, @name::text, @url::text, sqlc.narg(api_key)::bytea
from projects where projects.id = @project_id
returning *;

-- name: GetProvider :one
select * from providers where id = @id;

-- name: ReplaceProvider :one
-- The provider gets the name, the URL and the key that are given, in place
-- of all that it had.
update providers set name = @name, url = @url, api_key = sqlc.narg(api_key), updated_at = now()
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
insert into turns (id, session_id, seq, state, text)
select @id::uuid, sessions.id,
       (select coalesce(max(seq), 0) + 1 from turns where session_id = sessions.id),
       'queued', @text::text
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

-- name: DispatchTurn :exec
update turns set state = 'dispatching', token_hash = @token_hash, dispatched_at = now() where id = @id;

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
-- the provider of its agent as it is now, if the agent names its model.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id,
       agents.provider_id, coalesce(providers.url, '')::text as provider_url, providers.api_key as provider_api_key
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join providers on providers.id = agents.provider_id
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
