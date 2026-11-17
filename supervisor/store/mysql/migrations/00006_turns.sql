-- +goose Up
create table turns (
    id binary(16) primary key,
    session_id binary(16) not null,
    seq int not null,
    state varchar(32) not null,
    text mediumtext not null,
    created_at datetime(6) not null default (now(6)),
    -- When the turn was dispatched, when its harness was given the message,
    -- when it was asked to stop, and when it ended.
    dispatched_at datetime(6),
    started_at datetime(6),
    cancel_requested_at datetime(6),
    ended_at datetime(6),
    -- The hash of the token with which the turn's runner takes the turn, and
    -- the runner that took it, by the UUID that it made up for itself.
    token_hash varbinary(32),
    runner_id binary(16),
    -- Why the harness ended the turn, as the runner reported it.
    stop_reason mediumtext,
    -- Why the turn failed: a word for the kind of failure and what is known
    -- of it.
    failure_reason varchar(64),
    failure_message mediumtext,
    -- The token usage of the turn as its harness reported it.
    total_tokens bigint,
    input_tokens bigint,
    output_tokens bigint,
    thought_tokens bigint,
    cached_read_tokens bigint,
    cached_write_tokens bigint,
    foreign key (session_id) references sessions (id),
    unique key turns_session_seq (session_id, seq),
    unique key turns_token (token_hash),
    -- A session runs one turn at a time: no two of its turns are without an
    -- end. A turn that has ended counts for nothing here.
    unique key turns_unfinished_per_session ((case when ended_at is null then session_id end)),
    -- The turns of a state in the order in which they are dispatched.
    key turns_by_state (state, created_at, id)
);

-- +goose Down
drop table turns;
