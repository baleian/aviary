-- +goose Up
-- The secret stores of a project: where the project keeps secrets that it
-- does not give to Aviary to store.
create table secret_stores (
    id binary(16) primary key,
    project_id binary(16) not null,
    name mediumtext not null,
    -- The kind of the store, which says how it is asked.
    kind mediumtext not null,
    -- How the store is reached, as its kind has it, without what is secret.
    settings json not null,
    -- What the Supervisor logs in to the store with, sealed with a key that
    -- the database does not hold.
    credential blob not null,
    created_at datetime(6) not null default (now(6)),
    -- When the store was last given how it is reached.
    updated_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id)
);

-- +goose Down
drop table secret_stores;
