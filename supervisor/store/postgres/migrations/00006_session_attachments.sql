-- +goose Up
set local lock_timeout = '5s';
alter table sessions
    -- Whether the depot may have attachments of the session.
    add column has_attachments boolean not null default false;

-- +goose Down
set local lock_timeout = '5s';
alter table sessions drop column has_attachments;
