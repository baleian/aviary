package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/app"
	"aviary/supervisor/events"
	"aviary/supervisor/events/eventstest"
	"aviary/supervisor/store"
	"aviary/supervisor/store/storetest"
)

// database is a test's database, by its address.
type database string

func (d database) Open(t *testing.T) store.Store {
	records, err := plugins.Databases[0].Open(context.Background(), string(d))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(records.Close)
	return records
}

func (d database) Age(_ *testing.T, by time.Duration, turns ...uuid.UUID) {
	plugins.Databases[0].(*databases).database(string(d)).Age(by, turns...)
}

// The database plugin passes the store suite.
func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return database("memory://" + t.Name()) })
}

// The event log plugin passes the event log suite.
func TestLog(t *testing.T) {
	eventstest.Run(t, func(t *testing.T) events.Log {
		log, err := plugins.EventLogs[0].Open("memory://events")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(log.Close)
		return log
	})
}

// The build has exactly the plugins that it lists.
func TestBuild(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AVIARY_DATABASE_URL", "memory://records")
	if err := app.Run(ctx, []string{"migrate"}, plugins); err != nil {
		t.Errorf("migrate memory database: %v", err)
	}
	t.Setenv("AVIARY_DATABASE_URL", "postgres://user:secret@db.example/aviary")
	if err := app.Run(ctx, []string{"migrate"}, plugins); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("migrate database without a plugin: err = %v, want an error listing the memory scheme", err)
	}
	// Likewise for the event log.
	log, err := events.Open("memory://events", plugins.EventLogs...)
	if err != nil {
		t.Fatalf("open memory event log: %v", err)
	}
	log.Close()
	if _, err := events.Open("valkey://user:secret@events.example:6379", plugins.EventLogs...); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("open event log without a plugin: err = %v, want an error listing the memory scheme", err)
	}
}
