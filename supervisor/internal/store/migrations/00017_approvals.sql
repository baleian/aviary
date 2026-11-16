-- +goose Up
-- What the harness of a turn asked a person before it went on, and the
-- answer. A request waits for as long as its turn runs. It is resolved when
-- the harness has its answer: the option that a person chose or, for a turn
-- that was told to stop first, none.
create table approvals (
    turn_id uuid not null references turns (id),
    -- The name that the runner gave the request among those of its turn.
    id text not null,
    -- The request as the runner reported it: the tool call that the harness
    -- asks about and the options that it takes as an answer.
    request jsonb not null,
    requested_at timestamptz not null default now(),
    -- The option that a person chose, and when. The Supervisor that the
    -- runner of the turn is connected to passes it on.
    option_id text,
    answered_at timestamptz,
    -- How the harness got its answer, selected or cancelled, and when.
    outcome text,
    resolved_at timestamptz,
    primary key (turn_id, id)
);

-- The answers that are on their way to their harnesses, which every
-- Supervisor looks for again and again.
create index approvals_answered on approvals (turn_id) where answered_at is not null and resolved_at is null;

-- +goose Down
drop table approvals;
