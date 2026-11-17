-- +goose Up
-- The oldest Supervisor that still works on this schema, given as the schema
-- version that it was built for. A Supervisor built for an older version
-- does not start. A migration that takes away something that older
-- Supervisors use raises the value.
create table schema_floor (
    -- The table holds one row.
    singleton boolean primary key default true check (singleton),
    version bigint not null
);

insert into schema_floor (version) values (0);

-- +goose Down
drop table schema_floor;
