// A build of the Supervisor that is made outside the Supervisor's module,
// with plugins of its own. It is what someone has who connects the
// Supervisor to a database or an event log that Aviary has no plugin for: a
// module that depends on the Supervisor's, plugins that keep to its
// interfaces, and a main package that lists them. PLUGINS.md tells how to
// make one, and the tests of this module keep what it tells true.
//
// The plugins here keep everything in the memory of the process, which
// stands for the product that a plugin connects to.
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

// The plugins of this build. It brings its own for the database and for
// the event log, and takes the one for secret stores that Aviary has.
var plugins = app.Plugins{
	Databases:    []store.Plugin{&databases{}},
	EventLogs:    []events.Plugin{&logs{}},
	SecretStores: []secret.Plugin{kv2.Plugin{}},
}

func main() {
	app.Main(plugins)
}

// databases connects to the databases at memory://. Those who open the same
// address have the same database, as the Supervisors of an installation do.
type databases struct {
	mu sync.Mutex
	at map[string]*memorystore.Store
}

func (*databases) Schemes() []string { return []string{"memory"} }

func (d *databases) Open(_ context.Context, address string) (store.Store, error) {
	return d.database(address), nil
}

// database returns the database at the address.
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

// Migrate has nothing to do: a database in memory has no schema.
func (*databases) Migrate(context.Context, string) error { return nil }

func (*databases) MigrateTo(context.Context, string, int64) error { return nil }

// logs connects to the event logs at memory://, likewise.
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
