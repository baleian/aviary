-- +goose Up
-- What the agent said and did in a turn, in the order in which each item
-- began. It is put together from the events of the turn when the turn ends,
-- because the events are kept only for a while. An item is an object with a
-- type: a message or a thought with its whole text, a tool call as it last
-- stood, or the plan as it last stood.
create table turn_items (
    turn_id uuid not null references turns (id),
    seq integer not null,
    item jsonb not null,
    primary key (turn_id, seq)
);

-- +goose Down
drop table turn_items;
