-- +goose Up
-- The harness's own session, which holds the conversation. The harness
-- starts it in the first turn, and every later turn continues it.
alter table sessions add column harness_session_id text;
