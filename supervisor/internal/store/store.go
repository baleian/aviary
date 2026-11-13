// Package store keeps the Supervisor's records in PostgreSQL.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

var ErrNotFound = errors.New("not found")

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	pool *pgxpool.Pool
}

type Project struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Open connects to the database at url and brings its schema up to date.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Project{}, err
	}
	project := Project{ID: id, Name: name}
	err = s.pool.QueryRow(ctx,
		`insert into projects (id, name) values ($1, $2) returning created_at`,
		project.ID, project.Name,
	).Scan(&project.CreatedAt)
	return project, err
}

func (s *Store) Project(ctx context.Context, id uuid.UUID) (Project, error) {
	project := Project{ID: id}
	err := s.pool.QueryRow(ctx,
		`select name, created_at from projects where id = $1`, id,
	).Scan(&project.Name, &project.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

// migrate holds a lock in a table rather than on the connection, so that
// several Supervisors can start at once behind a connection pooler.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
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
	_, err = provider.Up(ctx)
	return err
}
