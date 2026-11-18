-- +goose Up
-- The user who chose the option. An answer of an installation that asks
-- nobody who they are is nobody's.
alter table approvals add column answered_by binary(16),
    add constraint approvals_answered_by foreign key (answered_by) references users (id);

-- +goose Down
alter table approvals drop foreign key approvals_answered_by, drop column answered_by;
