-- +goose Up
-- What the agent said and did in a turn, in the order in which each item
-- began. It is put together from the events of the turn when the turn ends,
-- because the events are kept only for a while.
create table turn_items (
    turn_id binary(16) not null,
    seq int not null,
    item json not null,
    primary key (turn_id, seq),
    foreign key (turn_id) references turns (id)
);

-- +goose Down
drop table turn_items;
