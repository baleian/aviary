-- +goose Up
set local lock_timeout = '5s';
-- The model that the turns of an agent use: a provider of the agent's
-- project, and the name of a model as that provider knows it. An agent that
-- was created without a model has none.
alter table agents add column provider_id uuid references providers (id);
alter table agents add column model text not null default '';

-- +goose Down
set local lock_timeout = '5s';
alter table agents drop column model;
alter table agents drop column provider_id;
