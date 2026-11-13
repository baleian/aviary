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

type Agent struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Harness   string    `json:"harness"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
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

// CreateAgent returns ErrNotFound if the project does not exist.
func (s *Store) CreateAgent(ctx context.Context, projectID uuid.UUID, harness string) (Agent, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Agent{}, err
	}
	agent := Agent{ID: id, ProjectID: projectID, Harness: harness}
	err = s.pool.QueryRow(ctx,
		`insert into agents (id, project_id, harness)
		 select $1, id, $3 from projects where id = $2
		 returning created_at`,
		agent.ID, agent.ProjectID, agent.Harness,
	).Scan(&agent.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return agent, err
}

func (s *Store) Agent(ctx context.Context, id uuid.UUID) (Agent, error) {
	agent := Agent{ID: id}
	err := s.pool.QueryRow(ctx,
		`select project_id, harness, created_at from agents where id = $1`, id,
	).Scan(&agent.ProjectID, &agent.Harness, &agent.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return agent, err
}

// CreateSession returns ErrNotFound if the agent does not exist.
func (s *Store) CreateSession(ctx context.Context, agentID uuid.UUID) (Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	session := Session{ID: id, AgentID: agentID}
	err = s.pool.QueryRow(ctx,
		`insert into sessions (id, agent_id)
		 select $1, id from agents where id = $2
		 returning created_at`,
		session.ID, session.AgentID,
	).Scan(&session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

func (s *Store) Session(ctx context.Context, id uuid.UUID) (Session, error) {
	session := Session{ID: id}
	err := s.pool.QueryRow(ctx,
		`select agent_id, created_at from sessions where id = $1`, id,
	).Scan(&session.AgentID, &session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
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
