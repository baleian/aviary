package main

import (
	"aviary/supervisor/events"
	"aviary/supervisor/events/resp"
	"aviary/supervisor/internal/secret"
	"aviary/supervisor/internal/secret/kv2"
	"aviary/supervisor/store"
	"aviary/supervisor/store/postgres"
)

// The plugins of this build: what connects the Supervisor to the database
// that holds its records, to the log of the turns' events and to the secret
// stores of the projects. A build for another kind of one of them adds its
// plugin here.
var (
	databases    = []store.Plugin{postgres.Plugin{}}
	eventLogs    = []events.Plugin{resp.Plugin{}}
	secretStores = []secret.Driver{kv2.Driver{}}
)
