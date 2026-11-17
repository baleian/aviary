package postgres_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
	"aviary/supervisor/store/storetest"
)

// database is a database of a test, by its URL.
type database string

func (d database) Open(t *testing.T) store.Store {
	return testenv.Open(t, string(d))
}

func (d database) Age(t *testing.T, by time.Duration, turns ...uuid.UUID) {
	testenv.Exec(t, string(d), `update turns set created_at = created_at - $2::interval, dispatched_at = dispatched_at - $2::interval,
		started_at = started_at - $2::interval, cancel_requested_at = cancel_requested_at - $2::interval,
		ended_at = ended_at - $2::interval where id = any($1)`, turns, by)
}

// The store on PostgreSQL does what every store is to do.
func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return database(testenv.Database(t)) })
}
