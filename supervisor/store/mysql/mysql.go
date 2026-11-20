// Package mysql implements store.Store on MySQL. The queries are in
// queries.sql, from which `make sqlc` generates internal/db.
package mysql

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Plugin is the store.Plugin for MySQL, whose URLs have the form
//
//	mysql://user:password@host:3306/name
//
// The only query parameter is tls. With tls=true the connection is encrypted
// and the server certificate is verified, tls=skip-verify encrypts without
// verifying, and tls=preferred encrypts if the server supports it.
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"mysql"} }

// connect returns a connection pool for the database at address. It does not
// connect yet.
func connect(address string) (*sql.DB, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		// A url.Error quotes the URL, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse the URL of the database: %w", err)
	}
	config := mysql.NewConfig()
	config.Net, config.Addr = "tcp", parsed.Host
	if parsed.Port() == "" {
		config.Addr = net.JoinHostPort(parsed.Hostname(), "3306")
	}
	config.User = parsed.User.Username()
	config.Passwd, _ = parsed.User.Password()
	config.DBName = strings.TrimPrefix(parsed.Path, "/")
	// Times are stored and read as UTC, whatever the server's time zone.
	config.ParseTime, config.Loc = true, time.UTC
	// Updates report the rows that they matched, unchanged ones included, so
	// that an unchanged row is not taken for a missing one.
	config.ClientFoundRows = true
	// The operations are written for READ COMMITTED, in which each statement
	// sees the latest committed rows, and a statement that waited for a row
	// lock reads the row again. MySQL defaults to REPEATABLE READ.
	config.Params = map[string]string{"time_zone": "'+00:00'", "transaction_isolation": "'READ-COMMITTED'"}
	// Use MySQL's default mode whatever the server's setting, so that a value
	// that does not fit its column is rejected rather than truncated.
	config.Params["sql_mode"] = "'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE," +
		"ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'"
	for name, values := range parsed.Query() {
		if name != "tls" {
			return nil, fmt.Errorf("the URL of the database has the parameter %q, and the only one that it may have is tls", name)
		}
		if !slices.Contains([]string{"true", "false", "skip-verify", "preferred"}, values[0]) {
			return nil, errors.New("the URL of the database may have tls=true, tls=false, tls=skip-verify or tls=preferred")
		}
		config.TLSConfig = values[0]
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, fmt.Errorf("the URL of the database: %w", err)
	}
	db := sql.OpenDB(connector)
	// Close connections before the server, or a proxy in between, does.
	db.SetConnMaxLifetime(4 * time.Minute)
	connections := max(4, runtime.NumCPU())
	db.SetMaxOpenConns(connections)
	db.SetMaxIdleConns(connections)
	return db, nil
}
