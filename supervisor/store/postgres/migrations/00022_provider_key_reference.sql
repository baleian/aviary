-- +goose Up
set local lock_timeout = '5s';
-- The API key of a provider may be in a secret store of the provider's
-- project and not here: the store, the path of the secret in it, and the
-- name of the value among those that the secret holds. Such a provider has
-- no api_key.
alter table providers add column api_key_store_id uuid references secret_stores (id);
alter table providers add column api_key_path text not null default '';
alter table providers add column api_key_name text not null default '';

-- +goose Down
set local lock_timeout = '5s';
alter table providers drop column api_key_name;
alter table providers drop column api_key_path;
alter table providers drop column api_key_store_id;
