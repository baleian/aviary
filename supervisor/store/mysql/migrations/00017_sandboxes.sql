-- +goose Up
-- The sandbox that the session runs in, as the sandbox service names it.
alter table sessions add column sandbox varchar(255);

-- +goose Down
alter table sessions drop column sandbox;
