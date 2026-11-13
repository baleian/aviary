-- +goose Up
create table agents (
    id uuid primary key,
    project_id uuid not null references projects (id),
    harness text not null,
    created_at timestamptz not null default now()
);

create table sessions (
    id uuid primary key,
    agent_id uuid not null references agents (id),
    created_at timestamptz not null default now()
);
