-- +goose Up
create table agents (
    id binary(16) primary key,
    project_id binary(16) not null,
    harness mediumtext not null,
    -- What the agent is told before every turn. An agent need not have any.
    instructions mediumtext not null default (''),
    -- What the pod of each turn of the agent asks for and may use at most,
    -- as the resources of a container are written for Kubernetes. An agent
    -- without resources of its own has none here.
    resources json,
    -- Who answers when the harness of the agent asks before it acts. 'ask'
    -- is a person, and nothing is asked of an agent without it.
    approval varchar(64) not null default '',
    -- The model that the turns of the agent use: a provider of the agent's
    -- project, and the name of a model as that provider knows it.
    provider_id binary(16),
    model mediumtext not null default (''),
    created_at datetime(6) not null default (now(6)),
    foreign key (project_id) references projects (id),
    foreign key (provider_id) references providers (id)
);

-- +goose Down
drop table agents;
