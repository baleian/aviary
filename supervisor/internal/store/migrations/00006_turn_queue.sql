-- +goose Up
-- The queued turns in the order in which they are dispatched.
create index turns_queued on turns (created_at, id) where state = 'queued';
