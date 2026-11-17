-- +goose Up
create table sessions (
    id binary(16) primary key,
    agent_id binary(16) not null,
    -- The harness's own session, which holds the conversation. The harness
    -- starts it in the first turn, and every later turn continues it.
    harness_session_id mediumtext,
    created_at datetime(6) not null default (now(6)),
    foreign key (agent_id) references agents (id)
);

-- +goose Down
drop table sessions;
