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

// fixture is a storetest.Database, given by its URL.
type fixture string

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
		exec(t, string(f), `update approvals set requested_at = requested_at - interval ? microsecond,
			answered_at = answered_at - interval ? microsecond, resolved_at = resolved_at - interval ? microsecond
			where turn_id = ?`, by.Microseconds(), by.Microseconds(), by.Microseconds(), key.ID(turn))
	}
}

func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return fixture(newDatabase(t)) })
}

// TestDeadlock checks that within retries a transaction that MySQL rolled
// back to break a deadlock.
func TestDeadlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := fixture(newDatabase(t)).Open(t).(*records)
	var attempts atomic.Int32
	var holding sync.WaitGroup
	holding.Add(2)
	// Each transaction locks one row and then the other's, which deadlocks.
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
				t.Errorf("deadlocked transaction: %v", err)
			}
		})
	}
	both.Wait()
	if attempts.Load() != 3 {
		t.Errorf("deadlocked transactions ran %d times, want 3", attempts.Load())
	}

	// A transaction that deadlocks every time is given up.
	attempts.Store(0)
	err := st.within(ctx, func(*db.Queries) error {
		attempts.Add(1)
		return &driver.MySQLError{Number: deadlock}
	})
	if !failed(err, deadlock) || attempts.Load() != 5 {
		t.Errorf("transaction that always deadlocks ran %d times, want 5: %v", attempts.Load(), err)
	}
	// Other errors are not retried.
	attempts.Store(0)
	if err := st.within(ctx, func(*db.Queries) error {
		attempts.Add(1)
		return &driver.MySQLError{Number: duplicateEntry}
	}); !failed(err, duplicateEntry) || attempts.Load() != 1 {
		t.Errorf("failed transaction ran %d times, want 1: %v", attempts.Load(), err)
	}
}
