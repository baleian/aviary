-- +goose Up
-- The model providers of a project: where the model endpoint of each is,
-- and the key to it. An agent of the project names the one that its turns
-- use.
create table providers (
    id uuid primary key,
    project_id uuid not null references projects (id),
    name text not null,
    -- The base URL of the endpoint.
    url text not null,
    -- The API key of the endpoint, sealed with a key that the database does
    -- not hold. An endpoint that takes no key has none.
    api_key bytea,
    created_at timestamptz not null default now(),
    -- When the URL and the key were last given.
    updated_at timestamptz not null default now()
);

-- +goose Down
drop table providers;
