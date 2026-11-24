-- +goose Up
alter table sessions
    -- Whether the depot may have attachments of the session.
    add column has_attachments boolean not null default false,
    algorithm = instant;

-- +goose Down
alter table sessions drop column has_attachments;
