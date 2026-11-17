-- +goose Up
-- What only one Supervisor does at a time. A Supervisor holds the row of it
-- for as long as its transaction lasts, and the others wait for the row:
-- dispatch while turns are dispatched, and queue while the turns that wait
-- are counted or told their positions.
create table locks (
    name varchar(32) primary key
);
insert into locks (name) values ('dispatch'), ('queue');

-- +goose Down
drop table locks;
