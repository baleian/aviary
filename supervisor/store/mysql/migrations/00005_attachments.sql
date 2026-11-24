-- +goose Up
-- A file that a message to a session carries, or that was uploaded for a
-- message to carry. Its bytes are an object of the depot.
create table attachments (
    id binary(16) primary key,
    session_id binary(16) not null,
    -- The turn of the message that carries the file, and the place of the
    -- file among those of the message. Null until a message carries it.
    turn_id binary(16),
    position int,
    -- The files of a message have different names. Compared exactly, not
    -- case-insensitively.
    name varchar(255) character set utf8mb4 collate utf8mb4_bin not null,
    media_type varchar(255) not null,
    size bigint not null,
    -- Who uploaded the file. Null if the request was not authenticated.
    created_by binary(16),
    created_at datetime(6) not null default (now(6)),
    foreign key (session_id) references sessions (id),
    foreign key (turn_id) references turns (id),
    foreign key (created_by) references users (id),
    -- For the files of a turn, and for those that no message carries by
    -- their age: these have no turn in the key.
    key attachments_turn (turn_id, created_at)
);

-- +goose Down
drop table attachments;
