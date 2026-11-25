// Command plugin is a build of the Supervisor outside its module, with
// plugins of its own, as for a database or an event log that Aviary has no
// plugin for. docs/src/content/docs/operate/plugins.md describes such a
// build, and the tests of this module keep it accurate.
//
// The plugins keep everything in memory, in place of the product that a real
// plugin connects to.
package main

import (
	"context"
	"sync"

	"aviary/supervisor/app"
	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/secret"
	"aviary/supervisor/secret/kv2"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// The build has its own plugins for the database and the event log, and
// Aviary's for secret stores.
var plugins = app.Plugins{
	Databases:    []store.Plugin{&databases{}},
	EventLogs:    []events.Plugin{&logs{}},
	SecretStores: []secret.Plugin{kv2.Plugin{}},
}

func main() {
	app.Main(plugins)
}

// databases opens in-memory databases at memory:// addresses. The same
// address opens the same database, as the Supervisors of an installation
// share one.
type databases struct {
	mu sync.Mutex
	at map[string]*memorystore.Store
}

func (*databases) Schemes() []string { return []string{"memory"} }

func (d *databases) Open(_ context.Context, address string) (store.Store, error) {
	return d.database(address), nil
}

func (d *databases) database(address string) *memorystore.Store {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.at[address] == nil {
		if d.at == nil {
			d.at = map[string]*memorystore.Store{}
		}
		d.at[address] = memorystore.New()
	}
	return d.at[address]
}

// Migrate is a no-op: an in-memory database has no schema.
func (*databases) Migrate(context.Context, string) error { return nil }

func (*databases) MigrateTo(context.Context, string, int64) error { return nil }

// logs opens in-memory event logs at memory:// addresses, likewise.
type logs struct {
	mu sync.Mutex
	at map[string]*memorylog.Log
}

func (*logs) Schemes() []string { return []string{"memory"} }

func (l *logs) Open(address string) (events.Log, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at[address] == nil {
		if l.at == nil {
			l.at = map[string]*memorylog.Log{}
		}
		l.at[address] = memorylog.New()
	}
	return l.at[address], nil
}
