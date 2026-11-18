-- name: CreateProject :exec
insert into projects (id, name) values (sqlc.arg(id), sqlc.arg(name));

-- name: GetProject :one
select * from projects where id = ?;

-- name: CreateAgent :execrows
insert into agents (id, project_id, harness, instructions, resources, approval, provider_id, model)
select sqlc.arg(id), projects.id, sqlc.arg(harness), sqlc.arg(instructions), sqlc.narg(resources), sqlc.arg(approval),
       sqlc.narg(provider_id), sqlc.arg(model)
from projects where projects.id = sqlc.arg(project_id);

-- name: GetAgent :one
select * from agents where id = ?;

-- name: CreateSession :execrows
insert into sessions (id, agent_id, created_by)
select sqlc.arg(id), agents.id, sqlc.narg(created_by) from agents where agents.id = sqlc.arg(agent_id);

-- name: GetSession :one
select * from sessions where id = ?;

-- name: LockSession :one
-- Holds the session until the transaction ends: the messages that reach a
-- session at once are taken one after another.
select id from sessions where id = ? for update;

-- name: CreateProvider :execrows
insert into providers (id, project_id, name, url, api_key, api_key_store_id, api_key_path, api_key_name)
select sqlc.arg(id), projects.id, sqlc.arg(name), sqlc.arg(url), sqlc.narg(api_key), sqlc.narg(api_key_store_id),
       sqlc.arg(api_key_path), sqlc.arg(api_key_name)
from projects where projects.id = sqlc.arg(project_id);

-- name: GetProvider :one
select * from providers where id = ?;

-- name: ReplaceProvider :execrows
-- The provider gets the name, the URL and the key that are given, in place
-- of all that it had. The key is stored, or in a secret store, or none.
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
-- The store gets the name, the kind, the settings and the credential that
-- are given, in place of all that it had.
update secret_stores
set name = sqlc.arg(name), kind = sqlc.arg(kind), settings = sqlc.arg(settings), credential = sqlc.arg(credential),
    updated_at = now(6)
where id = sqlc.arg(id);

-- name: Lock :one
-- Waits for the row of what only one Supervisor does at a time, and holds it
-- until the transaction ends.
select name from locks where name = ? for update;

-- name: NextSeq :one
select cast(coalesce(max(seq), 0) + 1 as signed) as seq from turns where session_id = ?;

-- name: CreateTurn :exec
insert into turns (id, session_id, seq, state, text, created_by)
values (sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(seq), 'queued', sqlc.arg(text), sqlc.narg(created_by));

-- name: GetTurn :one
select * from turns where id = ?;

-- name: LockTurn :one
-- Holds the turn until the transaction ends: what is done to a turn at once
-- is done one after another.
select * from turns where id = ? for update;

-- name: LockTurnOfToken :one
select * from turns where token_hash = ? for update;

-- name: ListTurns :many
-- The turns of a session that came before the one with the sequence number,
-- newest first.
select * from turns where session_id = sqlc.arg(session_id) and seq < sqlc.arg(before) order by seq desc limit ?;

-- name: GetTurnHarness :one
select agents.harness
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
where turns.id = ?;

-- name: CountQueuedTurns :one
select count(*) from turns where state = 'queued';

-- name: CountDispatchedTurns :one
select count(*) from turns where state in ('dispatching', 'running');

-- name: QueuedTurns :many
select id from turns where state = 'queued' order by created_at, id;

-- name: CancellingTurns :many
select id from turns where ended_at is null and cancel_requested_at is not null;

-- name: NextTurns :many
-- The queued turns, oldest first, and what it takes to run each. A turn of
-- an agent that names its model has the URL of the agent's provider and the
-- name of the model.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id, agents.harness,
       agents.resources, agents.approval, coalesce(providers.url, '') as provider_url, agents.model,
       cast(timestampdiff(microsecond, turns.created_at, now(6)) as signed) as waited
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join providers on providers.id = agents.provider_id
where turns.state = 'queued'
order by turns.created_at, turns.id
limit ?;

-- name: DispatchTurn :execrows
-- A turn that was cancelled since it was read is left as it is.
update turns set state = 'dispatching', token_hash = sqlc.arg(token_hash), dispatched_at = now(6)
where id = sqlc.arg(id) and state = 'queued';

-- name: DispatchedTurns :many
select id, state,
       cast(timestampdiff(microsecond, coalesce(dispatched_at, created_at), now(6)) as signed) as age,
       cast(coalesce(timestampdiff(microsecond, cancel_requested_at, now(6)), 0) as signed) as cancelling
from turns where state in ('dispatching', 'running');

-- name: TakeTurn :exec
update turns set state = 'running', runner_id = sqlc.narg(runner_id) where id = sqlc.arg(id);

-- name: SessionOfTurn :one
-- What a runner is given of the session of its turn.
select sessions.harness_session_id, agents.harness, agents.instructions
from sessions
join agents on agents.id = sessions.agent_id
where sessions.id = ?;

-- name: RunningTurn :one
-- The running turn that the token belongs to, what the turn belongs to, and
-- the provider of its agent as it is now, if the agent names its model. A
-- provider whose key is in a secret store comes with that store, as it is
-- now.
select turns.id as turn_id, sessions.id as session_id, agents.id as agent_id, agents.project_id,
       agents.provider_id, coalesce(providers.url, '') as provider_url, providers.api_key as provider_api_key,
       providers.api_key_store_id, coalesce(providers.api_key_path, '') as api_key_path,
       coalesce(providers.api_key_name, '') as api_key_name,
       coalesce(secret_stores.name, '') as store_name, coalesce(secret_stores.kind, '') as store_kind,
       secret_stores.settings as store_settings, secret_stores.credential as store_credential,
       secret_stores.created_at as store_created_at, secret_stores.updated_at as store_updated_at
from turns
join sessions on sessions.id = turns.session_id
join agents on agents.id = sessions.agent_id
left join providers on providers.id = agents.provider_id
left join secret_stores on secret_stores.id = providers.api_key_store_id
where turns.token_hash = ? and turns.state = 'running';

-- name: StartTurn :exec
update turns set started_at = now(6) where id = ?;

-- name: SetHarnessSession :exec
update sessions set harness_session_id = sqlc.arg(harness_session_id) where id = sqlc.arg(id);

-- name: Startup :one
-- How long the turn took to start, in microseconds.
select cast(coalesce(timestampdiff(microsecond, dispatched_at, started_at), 0) as signed) as startup from turns where id = ?;

-- name: SetFinished :exec
update turns set stop_reason = sqlc.arg(stop_reason), total_tokens = sqlc.narg(total_tokens),
       input_tokens = sqlc.narg(input_tokens), output_tokens = sqlc.narg(output_tokens),
       thought_tokens = sqlc.narg(thought_tokens), cached_read_tokens = sqlc.narg(cached_read_tokens),
       cached_write_tokens = sqlc.narg(cached_write_tokens)
where id = sqlc.arg(id);

-- name: CancelQueuedTurn :exec
-- A queued turn ends at once.
update turns set state = 'cancelled', ended_at = now(6), cancel_requested_at = coalesce(cancel_requested_at, now(6))
where id = ?;

-- name: AskTurnToStop :exec
-- A dispatched turn is only marked.
update turns set cancel_requested_at = coalesce(cancel_requested_at, now(6)) where id = ?;

-- name: LockTurnsToExpire :many
select id from turns
where state = 'queued' and created_at < now(6) - interval sqlc.arg(microseconds) microsecond
order by created_at, id
for update;

-- name: ExpireTurn :exec
update turns set state = 'expired', ended_at = now(6) where id = ?;

-- name: GiveUpTurn :execrows
update turns set failure_reason = sqlc.arg(reason), failure_message = nullif(sqlc.arg(message), '')
where id = sqlc.arg(id) and state = 'dispatching' and failure_reason is null and cancel_requested_at is null;

-- name: EndTurn :exec
update turns set ended_at = now(6), state = sqlc.arg(state), failure_reason = sqlc.narg(failure_reason),
       failure_message = nullif(sqlc.narg(failure_message), '')
where id = sqlc.arg(id);

-- name: AddTurnItem :exec
insert into turn_items (turn_id, seq, item) values (?, ?, ?);

-- name: ListTurnItems :many
select turn_id, item from turn_items where turn_id in (sqlc.slice('turn_ids')) order by turn_id, seq;

-- name: AddApproval :exec
-- A request that its runner reports again is there already.
insert into approvals (turn_id, id, request) values (?, ?, ?)
on duplicate key update turn_id = turn_id;

-- name: GetApproval :one
select * from approvals where turn_id = ? and id = ?;

-- name: ResolveApproval :execrows
-- The harness has its answer. A request that was resolved before is left as
-- it is.
update approvals set outcome = sqlc.arg(outcome), resolved_at = now(6)
where turn_id = sqlc.arg(turn_id) and id = sqlc.arg(id) and resolved_at is null;

-- name: AnswerApproval :execrows
-- Keeps the option that a person chose, for a request that has no answer
-- yet and whose turn has not ended.
update approvals set option_id = sqlc.arg(option_id), answered_at = now(6)
where approvals.turn_id = sqlc.arg(turn_id) and approvals.id = sqlc.arg(id)
  and approvals.answered_at is null and approvals.resolved_at is null
  and exists (select 1 from turns where turns.id = approvals.turn_id and turns.ended_at is null);

-- name: WaitingApprovals :many
-- The requests of a turn that wait for a person, oldest first.
select request from approvals
where turn_id = ? and answered_at is null and resolved_at is null
order by requested_at, id;

-- name: Answers :many
-- The answers that the harnesses of turns that run have not got yet: those
-- of one turn, or of all.
select approvals.turn_id, approvals.id, coalesce(approvals.option_id, '') as option_id
from approvals
join turns on turns.id = approvals.turn_id
where approvals.answered_at is not null and approvals.resolved_at is null and turns.ended_at is null
  and (sqlc.narg(turn_id) is null or approvals.turn_id = sqlc.narg(turn_id));

-- name: GetUserBySubject :one
select * from users where subject = ?;

-- name: FreeUsername :exec
-- The username is that of the user whom the provider gives it to now.
update users set username = null where username = sqlc.arg(username) and subject <> sqlc.arg(subject);

-- name: CreateUser :exec
insert into users (id, subject, username, display_name) values (?, ?, ?, ?);

-- name: UpdateUser :exec
-- A user that is given no username keeps the one that it has.
update users set username = coalesce(sqlc.narg(username), username), display_name = sqlc.arg(display_name)
where subject = sqlc.arg(subject);

-- name: ListRoles :many
select * from roles order by name;

-- name: GetRole :one
select * from roles where name = ?;

-- name: SetRole :exec
update roles set permissions = sqlc.arg(permissions), updated_at = now(6), updated_by = sqlc.narg(updated_by)
where name = sqlc.arg(name);
