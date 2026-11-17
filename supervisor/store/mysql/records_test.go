package mysql

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"aviary/supervisor/store"
	"aviary/supervisor/store/mysql/internal/db"
	"aviary/supervisor/store/mysql/internal/key"
	"aviary/supervisor/store/storetest"
)

// fixture is the database of a test of the suite, by its URL.
type fixture string

// Open returns a store on the database, as one Supervisor has it. Several
// Supervisors are several stores on one database.
func (f fixture) Open(t *testing.T) store.Store {
	t.Helper()
	st, err := Plugin{}.Open(context.Background(), string(f))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func (f fixture) Age(t *testing.T, by time.Duration, turns ...uuid.UUID) {
	t.Helper()
	for _, turn := range turns {
		exec(t, string(f), `update turns set created_at = created_at - interval ? microsecond,
			dispatched_at = dispatched_at - interval ? microsecond, started_at = started_at - interval ? microsecond,
			cancel_requested_at = cancel_requested_at - interval ? microsecond, ended_at = ended_at - interval ? microsecond
			where id = ?`, by.Microseconds(), by.Microseconds(), by.Microseconds(), by.Microseconds(), by.Microseconds(), key.ID(turn))
	}
}

// The store on MySQL does what every store is to do.
func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return fixture(newDatabase(t)) })
}

// A transaction that MySQL undoes because it and another waited for each
// other is run again.
func TestDeadlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := fixture(newDatabase(t)).Open(t).(*records)
	var attempts atomic.Int32
	var holding sync.WaitGroup
	holding.Add(2)
	// Each holds one row and then asks for the row that the other holds.
	orders := [][2]string{{dispatchLock, queueLock}, {queueLock, dispatchLock}}
	var both sync.WaitGroup
	for _, order := range orders {
		both.Go(func() {
			var held sync.Once
			err := st.within(ctx, func(queries *db.Queries) error {
				attempts.Add(1)
				if _, err := queries.Lock(ctx, order[0]); err != nil {
					return err
				}
				held.Do(holding.Done)
				holding.Wait()
				_, err := queries.Lock(ctx, order[1])
				return err
			})
			if err != nil {
				t.Errorf("a transaction that waited for one that waited for it: %v", err)
			}
		})
	}
	both.Wait()
	if attempts.Load() != 3 {
		t.Errorf("two transactions that waited for each other ran %d times", attempts.Load())
	}

	// One that is undone every time is given up.
	attempts.Store(0)
	err := st.within(ctx, func(*db.Queries) error {
		attempts.Add(1)
		return &driver.MySQLError{Number: deadlock}
	})
	if !failed(err, deadlock) || attempts.Load() != 5 {
		t.Errorf("a transaction that is undone every time ran %d times: %v", attempts.Load(), err)
	}
	// And one that fails with something else is not run again.
	attempts.Store(0)
	if err := st.within(ctx, func(*db.Queries) error {
		attempts.Add(1)
		return &driver.MySQLError{Number: duplicateEntry}
	}); !failed(err, duplicateEntry) || attempts.Load() != 1 {
		t.Errorf("a transaction that failed ran %d times: %v", attempts.Load(), err)
	}
}
