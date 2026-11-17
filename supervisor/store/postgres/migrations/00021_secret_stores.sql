-- +goose Up
-- The secret stores of a project: where the project keeps secrets that it
-- does not give to Aviary to store. A secret of the project, such as the API
-- key of a provider, may be a place in one of them.
create table secret_stores (
    id uuid primary key,
    project_id uuid not null references projects (id),
    name text not null,
    -- The kind of the store, which says how it is asked. The Supervisor has
    -- a driver for each kind that it knows.
    kind text not null,
    -- How the store is reached, as its kind has it, without what is secret.
    settings jsonb not null,
    -- What the Supervisor logs in to the store with, sealed with a key that
    -- the database does not hold.
    credential bytea not null,
    created_at timestamptz not null default now(),
    -- When the store was last given how it is reached.
    updated_at timestamptz not null default now()
);

-- +goose Down
drop table secret_stores;
