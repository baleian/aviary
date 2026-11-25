package postgres

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

// Only Migrate changes the schema. Open only checks it, so that Supervisors of
// two releases can share a database; see
// docs/src/content/docs/operate/upgrades.md.

//go:embed migrations/*.sql
var migrations embed.FS

// undefinedTable is the PostgreSQL error code for a table that does not exist.
const undefinedTable = "42P01"

// Migrate brings the schema of the database at address up to date.
func (p Plugin) Migrate(ctx context.Context, address string) error {
	latest, err := latestMigration()
	if err != nil {
		return err
	}
	return p.MigrateTo(ctx, address, latest)
}

// MigrateTo migrates the schema of the database at address to version, forward
// or back. Migrating back drops what the newer migrations added, with its
// data; rolling back a release does not need it.
func (Plugin) MigrateTo(ctx context.Context, address string, version int64) error {
	latest, err := latestMigration()
	if err != nil {
		return err
	}
	if version < 0 || version > latest {
		return fmt.Errorf("schema version %d does not exist; latest is %d", version, latest)
	}
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	// A lock in a table, unlike one on the connection, also works behind a
	// connection pooler.
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
	slog.InfoContext(ctx, "database schema migrated", "version", version)
	return nil
}

// checkSchema returns an error if the schema is older than this build needs,
// or if its floor excludes this build. A newer schema is accepted otherwise,
// since migrations only add to it until they raise the floor.
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
		return fmt.Errorf("read schema version: %w", err)
	}
	if applied < needed {
		return fmt.Errorf("database schema is at version %d, but this Supervisor needs version %d: run `aviary-supervisor migrate` first", applied, needed)
	}
	var floor int64
	if err := pool.QueryRow(ctx, `select version from schema_floor`).Scan(&floor); err != nil {
		return fmt.Errorf("read schema floor: %w", err)
	}
	if needed < floor {
		return fmt.Errorf("this Supervisor is built for schema version %d, but database schema version %d requires a build for version %d or later", needed, applied, floor)
	}
	if applied > needed {
		slog.InfoContext(ctx, "database schema is newer than this Supervisor", "schema", applied, "needed", needed)
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
