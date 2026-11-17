// Package mysql keeps the Supervisor's records in MySQL.
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

// Plugin connects the Supervisor to MySQL databases, whose addresses begin
// with mysql://:
//
//	mysql://user:password@host:3306/name
//
// With ?tls=true the connection is encrypted and the certificate of the
// server is checked. tls=skip-verify encrypts without the check, and
// tls=preferred encrypts if the server can.
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"mysql"} }

// connect returns the connections to the database at the address. None is
// made before it is used.
func connect(address string) (*sql.DB, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		// A URL that cannot be parsed is quoted in the error, password included.
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
	// The times are kept and read as UTC, whatever the time zone of the
	// server is.
	config.ParseTime, config.Loc = true, time.UTC
	// An update counts the rows that it found, also those that it left as
	// they were: a row that is there is not taken for one that is not.
	config.ClientFoundRows = true
	// A statement sees what is committed when it runs, and what waits for a
	// row looks at the row again when it has it. That is not the default of
	// MySQL, and it is what the operations are written for.
	config.Params = map[string]string{"time_zone": "'+00:00'", "transaction_isolation": "'READ-COMMITTED'"}
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
	// A connection is closed here before the server, or what is between,
	// closes it.
	db.SetConnMaxLifetime(4 * time.Minute)
	connections := max(4, runtime.NumCPU())
	db.SetMaxOpenConns(connections)
	db.SetMaxIdleConns(connections)
	return db, nil
}
