-- +goose Up
set local lock_timeout = '5s';
-- When the session's sandbox was last used apart from its turns, as when its
-- files were read. Null if it never was.
alter table sessions add column sandbox_used_at timestamptz;

-- +goose Down
set local lock_timeout = '5s';
alter table sessions drop column sandbox_used_at;
