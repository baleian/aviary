package main

import (
	"aviary/supervisor/app"
	"aviary/supervisor/events"
	"aviary/supervisor/events/resp"
	"aviary/supervisor/identity"
	"aviary/supervisor/identity/oidc"
	"aviary/supervisor/secret"
	"aviary/supervisor/secret/kv2"
	"aviary/supervisor/store"
	"aviary/supervisor/store/mysql"
	"aviary/supervisor/store/postgres"
)

// The plugins of this build: what connects the Supervisor to the database
// that holds its records, to the log of the turns' events, to the secret
// stores of the projects and to the identity provider that tells who sends a
// request. A build for another kind of one of them adds its plugin here.
var plugins = app.Plugins{
	Databases:    []store.Plugin{postgres.Plugin{}, mysql.Plugin{}},
	EventLogs:    []events.Plugin{resp.Plugin{}},
	SecretStores: []secret.Plugin{kv2.Plugin{}},

	IdentityProviders: []identity.Plugin{oidc.Plugin{}},
}
