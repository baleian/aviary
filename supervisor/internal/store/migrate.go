package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// The schema is changed by Migrate, which a release runs once before it
// starts its Supervisors. A Supervisor does not change the schema. It only
// checks that the schema is not older than it needs, so that Supervisors of
// two releases can run on one database: while a release is rolled out, after
// it is rolled back, or side by side.

//go:embed migrations/*.sql
var migrations embed.FS

// undefinedTable is the PostgreSQL error code for a table that does not exist.
const undefinedTable = "42P01"

// Migrate brings the schema of the database at url up to date.
func Migrate(ctx context.Context, url string) error {
	latest, err := latestMigration()
	if err != nil {
		return err
	}
	return MigrateTo(ctx, url, latest)
}

// MigrateTo brings the schema of the database at url to the given version,
// forward or back. Going back is for the rare case that a schema change
// itself has to be undone: it drops what the newer migrations added, with
// the data in it. Going back to an earlier release does not need it.
func MigrateTo(ctx context.Context, url string, version int64) error {
	latest, err := latestMigration()
	if err != nil {
		return err
	}
	if version < 0 || version > latest {
		return fmt.Errorf("there is no schema version %d; the newest is %d", version, latest)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	// The lock is held in a table rather than on the connection, so that it
	// also holds behind a connection pooler.
	locker, err := lock.NewPostgresTableLocker()
	if err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithLocker(locker))
	if err != nil {
		return err
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
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

// checkSchema reports an error if the schema is older than this build needs.
// A newer schema is accepted: the migrations keep the schema usable by the
// release before the one that applied them.
func checkSchema(ctx context.Context, pool *pgxpool.Pool) error {
	needed, err := latestMigration()
	if err != nil {
		return err
	}
	var applied int64
	err = pool.QueryRow(ctx, `select coalesce(max(version_id), 0) from goose_db_version`).Scan(&applied)
	var failure *pgconn.PgError
	if errors.As(err, &failure) && failure.Code == undefinedTable {
		applied, err = 0, nil
	}
	if err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	if applied < needed {
		return fmt.Errorf("the database schema is at version %d and this Supervisor needs %d: run `aviary-supervisor migrate` first", applied, needed)
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
