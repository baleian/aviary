package memory_test

import (
	"testing"

	"aviary/supervisor/events"
	"aviary/supervisor/events/eventstest"
	"aviary/supervisor/events/memory"
)

// The log in memory is a log like any other: the tests of the Supervisor
// that use it tell of a Supervisor with a log.
func TestLog(t *testing.T) {
	log := memory.New()
	eventstest.Run(t, func(*testing.T) events.Log { return log })
}
