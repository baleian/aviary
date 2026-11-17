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

// database is the database of a test, by its address.
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

// A plugin for a database passes what holds of every store.
func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Database { return database("memory://" + t.Name()) })
}

// A plugin for an event log passes what holds of every log.
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

// The build is a Supervisor with the plugins that it lists, and with no
// others.
func TestBuild(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AVIARY_DATABASE_URL", "memory://records")
	if err := app.Run(ctx, []string{"migrate"}, plugins); err != nil {
		t.Errorf("the build migrated its database with: %v", err)
	}
	t.Setenv("AVIARY_DATABASE_URL", "postgres://user:secret@db.example/aviary")
	if err := app.Run(ctx, []string{"migrate"}, plugins); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("the build migrated a database that it has no plugin for with: %v", err)
	}
	// So it is with its event log.
	log, err := events.Open("memory://events", plugins.EventLogs...)
	if err != nil {
		t.Fatalf("the build opened its event log with: %v", err)
	}
	log.Close()
	if _, err := events.Open("valkey://user:secret@events.example:6379", plugins.EventLogs...); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("the build opened an event log that it has no plugin for with: %v", err)
	}
}
