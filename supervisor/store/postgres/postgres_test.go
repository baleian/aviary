package postgres_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
	"aviary/supervisor/store/postgres"
	"aviary/supervisor/store/storetest"
)

// emptyDatabase creates a database for the test and returns its URL. The
// database is dropped when the test ends.
func emptyDatabase(t *testing.T) string {
	t.Helper()
	server := testenv.DatabaseURL(t, "postgres")
	address, err := url.Parse(server)
	if err != nil {
		t.Fatalf("parse database server URL: %v", err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	exec(t, server, "create database "+name)
	t.Cleanup(func() { exec(t, server, "drop database "+name+" with (force)") })
	address.Path = "/" + name
	return address.String()
}

// newDatabase is like emptyDatabase, with the schema of this build.
func newDatabase(t *testing.T) string {
	t.Helper()
	database := emptyDatabase(t)
	if err := (postgres.Plugin{}).Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// open opens a Store on the database. Each call stands for one more
// Supervisor.
func open(t *testing.T, database string) store.Store {
	t.Helper()
	st, err := postgres.Plugin{}.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

// exec runs a statement on the database, to arrange what a Store cannot, such
// as the passing of time or the schema of another release.
func exec(t *testing.T, database, statement string, arguments ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, database)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, statement, arguments...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// fixture is a storetest.Database, given by its URL.
type fixture string

func (f fixture) Open(t *testing.T) store.Store {
	return open(t, string(f))
}

func (f fixture) Age(t *testing.T, by time.Duration, turns ...uuid.UUID) {
	exec(t, string(f), `update turns set created_at = created_at - $2::interval, dispatched_at = dispatched_at - $2::interval,
		started_at = started_at - $2::interval, cancel_requested_at = cancel_requested_at - $2::interval,
		ended_at = ended_at - $2::interval where id = any($1)`, turns, by)
	exec(t, string(f), `update approvals set requested_at = requested_at - $2::interval, answered_at = answered_at - $2::interval,
		resolved_at = resolved_at - $2::interval where turn_id = any($1)`, turns, by)
}

func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return fixture(newDatabase(t)) })
}

// What the Supervisors do for every message, request and sweep finds the
// unfinished turns and the sessions with a sandbox through indexes: it reads
// no table from end to end, however many sessions and turns have ended.
func TestRoutineSkipsEndedRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newDatabase(t)
	exec(t, database, `
		with project as (
			insert into projects (id, slug) values (gen_random_uuid(), 'ended') returning id
		), provider as (
			insert into providers (id, project_id, name, url)
			select gen_random_uuid(), id, 'ended', 'https://api.example.com/v1' from project returning id, project_id
		), agent as (
			insert into agents (id, project_id, slug, harness, provider_id, model)
			select gen_random_uuid(), project_id, 'ended', 'opencode', id, 'a-model' from provider returning id
		), session as (
			insert into sessions (id, agent_id, ended_at)
			select gen_random_uuid(), agent.id, now() from agent, generate_series(1, 20000) returning id
		), turn as (
			insert into turns (id, session_id, seq, state, text, ended_at)
			select gen_random_uuid(), id, 1, 'completed', 'hello', now() from session returning id
		), item as (
			insert into turn_items (turn_id, seq, item) select id, 1, '{}' from turn
		)
		insert into approvals (turn_id, id, request, option_id, answered_at, outcome, resolved_at)
		select id, '1', '{}', 'once', now(), 'selected', now() from turn`)
	exec(t, database, "analyze")
	exec(t, database, "select pg_stat_reset()")

	st, err := postgres.Plugin{}.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	storetest.Routine(t, st)
	// A connection adds what it read to the statistics when it closes.
	st.Close()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		others := rows(t, database, "select count(*) from pg_stat_activity where datname = current_database() and pid <> pg_backend_pid()")
		if others[0] == "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s connections to the database are still open", others[0])
		}
	}
	const growing = "relname in ('sessions', 'turns', 'turn_items', 'approvals')"
	if counted := rows(t, database, "select relname from pg_stat_user_tables where idx_scan > 0 and "+growing); len(counted) != 4 {
		t.Fatalf("the statistics count index scans of %v, want of all four tables", counted)
	}
	if read := rows(t, database, "select relname from pg_stat_user_tables where seq_scan > 0 and "+growing); len(read) != 0 {
		t.Errorf("the routine read %v from end to end", read)
	}
}
