-- +goose Up
alter table turns
    -- The hash of the token with which the turn's runner takes the turn.
    add column token_hash bytea unique,
    -- Why the harness ended the turn, as the runner reported it.
    add column stop_reason text;
