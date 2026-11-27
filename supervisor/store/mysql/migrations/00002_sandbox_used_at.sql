-- +goose Up
-- When the session's sandbox was last used apart from its turns, as when its
-- files were read. Null if it never was.
alter table sessions add column sandbox_used_at datetime(6), algorithm = instant;

-- +goose Down
alter table sessions drop column sandbox_used_at;
