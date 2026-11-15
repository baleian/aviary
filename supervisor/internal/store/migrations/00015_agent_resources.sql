-- +goose Up
set local lock_timeout = '5s';

-- What the pod of each turn of the agent asks for and may use at most, as
-- the resources of a container are written for Kubernetes. An agent without
-- resources of its own has none here: its turns get the default of its
-- harness, as that is when each turn starts.
alter table agents add column resources jsonb;

-- +goose Down
set local lock_timeout = '5s';

alter table agents drop column resources;
