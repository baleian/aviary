-- +goose Up
-- The model providers of a project: where the model endpoint of each is,
-- and the key to it. An agent of the project names the one that its turns
-- use.
create table providers (
    id binary(16) primary key,
    project_id binary(16) not null,
    name mediumtext not null,
    -- The base URL of the endpoint.
    url mediumtext not null,
    -- The API key of the endpoint, sealed with a key that the database does
    -- not hold. An endpoint that takes no key has none, and neither has a
    -- provider whose key is in a secret store of the project: that one has
    -- the store, the path of the secret in it, and the name of the value
    -- among those that the secret holds.
    api_key blob,
    api_key_store_id binary(16),
    api_key_path mediumtext not null default (''),
    api_key_name mediumtext not null default (''),
    created_at datetime(6) not null default (now(6)),
    -- When the URL and the key were last given.
    updated_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id),
    foreign key (api_key_store_id) references secret_stores (id)
);

-- +goose Down
drop table providers;
