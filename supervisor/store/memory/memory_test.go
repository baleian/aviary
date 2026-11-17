package memory_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
	"aviary/supervisor/store/memory"
	"aviary/supervisor/store/storetest"
)

// database is a store in memory as the database of a test: its Supervisors
// share the one store.
type database struct{ records *memory.Store }

func (d database) Open(*testing.T) store.Store { return d.records }

func (d database) Age(_ *testing.T, by time.Duration, turns ...uuid.UUID) {
	d.records.Age(by, turns...)
}

func TestStore(t *testing.T) {
	storetest.Run(t, func(*testing.T) storetest.Database { return database{memory.New()} })
}
