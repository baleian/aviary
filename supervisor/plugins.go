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

// The plugins of this build. To support another database, event log, secret
// store or identity provider, add its plugin here.
var plugins = app.Plugins{
	Databases:    []store.Plugin{postgres.Plugin{}, mysql.Plugin{}},
	EventLogs:    []events.Plugin{resp.Plugin{}},
	SecretStores: []secret.Plugin{kv2.Plugin{}},

	IdentityProviders: []identity.Plugin{oidc.Plugin{}},
}
