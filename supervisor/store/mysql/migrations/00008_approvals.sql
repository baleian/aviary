-- +goose Up
-- What the harness of a turn asked a person before it went on, and the
-- answer. A request waits for as long as its turn runs. It is resolved when
-- the harness has its answer: the option that a person chose or, for a turn
-- that was told to stop first, none.
create table approvals (
    turn_id binary(16) not null,
    -- The name that the runner gave the request among those of its turn,
    -- which is the same name only if it is the same letter for letter.
    id varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    -- The request as the runner reported it.
    request json not null,
    requested_at datetime(6) not null default (now(6)),
    -- The option that a person chose, and when.
    option_id mediumtext,
    answered_at datetime(6),
    -- How the harness got its answer, selected or cancelled, and when.
    outcome varchar(64),
    resolved_at datetime(6),
    primary key (turn_id, id),
    foreign key (turn_id) references turns (id)
);

-- +goose Down
drop table approvals;
