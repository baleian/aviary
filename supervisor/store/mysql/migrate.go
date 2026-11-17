package mysql

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// The schema is changed by Migrate, which a release runs once before it
// starts its Supervisors. A Supervisor does not change the schema. It only
// checks that the schema is not older than it needs, so that Supervisors of
// two releases can run on one database: while a release is rolled out, after
// it is rolled back, or side by side.
//
// MySQL does not undo a change of the schema that failed halfway, so a
// migration changes one thing.

//go:embed migrations/*.sql
var migrations embed.FS

// undefinedTable is the MySQL error number for a table that does not exist.
const undefinedTable = 1146

// Migrate brings the schema of the database at the address up to date.
func (p Plugin) Migrate(ctx context.Context, address string) error {
	latest, err := latestMigration()
	if err != nil {
		return err
	}
	return p.MigrateTo(ctx, address, latest)
}

// MigrateTo brings the schema of the database at the address to the given
// version, forward or back. Going back is for the rare case that a schema
// change itself has to be undone: it drops what the newer migrations added,
// with the data in it. Going back to an earlier release does not need it.
func (Plugin) MigrateTo(ctx context.Context, address string, version int64) error {
	latest, err := latestMigration()
	if err != nil {
		return err
	}
	if version < 0 || version > latest {
		return fmt.Errorf("there is no schema version %d; the newest is %d", version, latest)
	}
	db, err := connect(address)
	if err != nil {
		return err
	}
	defer db.Close()
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	// The lock is held in a table rather than on the connection, so that it
	// also holds behind a connection pooler.
	locker, err := lock.NewMySQLTableLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectMySQL, db, files, goose.WithLocker(locker))
	if err != nil {
		return err
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	move := provider.UpTo
	if version < current {
		move = provider.DownTo
	}
	moved, err := move(ctx, version)
	for _, migration := range moved {
		slog.InfoContext(ctx, "ran migration", "version", migration.Source.Version,
			"direction", migration.Direction, "duration", migration.Duration.String())
	}
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	slog.InfoContext(ctx, "database schema is at version", "version", version)
	return nil
}

// checkSchema reports an error if this build and the schema do not go
// together: if the schema is older than the build needs, or if it has since
// dropped something that the build uses. A newer schema is accepted
// otherwise, because migrations only add to the schema until they raise the
// floor.
func checkSchema(ctx context.Context, db *sql.DB) error {
	needed, err := latestMigration()
	if err != nil {
		return err
	}
	var applied int64
	err = db.QueryRowContext(ctx, `select coalesce(max(version_id), 0) from goose_db_version`).Scan(&applied)
	var failure *mysql.MySQLError
	if errors.As(err, &failure) && failure.Number == undefinedTable {
		applied, err = 0, nil
	}
	if err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	if applied < needed {
		return fmt.Errorf("the database schema is at version %d and this Supervisor needs %d: run `aviary-supervisor migrate` first", applied, needed)
	}
	var floor int64
	if err := db.QueryRowContext(ctx, `select version from schema_floor`).Scan(&floor); err != nil {
		return fmt.Errorf("read the schema floor: %w", err)
	}
	if needed < floor {
		return fmt.Errorf("this Supervisor is built for schema version %d, and the database schema, at version %d, no longer supports Supervisors built for a version before %d", needed, applied, floor)
	}
	if applied > needed {
		slog.InfoContext(ctx, "the database schema is newer than this Supervisor", "schema", applied, "needed", needed)
	}
	return nil
}

// latestMigration returns the version of the newest migration in this build.
func latestMigration() (int64, error) {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, entry := range entries {
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			return 0, err
		}
		latest = max(latest, version)
	}
	return latest, nil
}
