// Package store provides the database access layer for the API: the
// embedded schema migrations and a pgx connection pool wrapper. Query
// methods for specific domain objects (users, titles, guides, ...) hang off
// *Store in later tasks.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// Registers the "pgx" database/sql driver used by Migrate below. Store
	// itself talks to Postgres through a pgxpool.Pool (see store.go); this
	// stdlib shim exists solely so golang-migrate can drive schema changes
	// over database/sql without pulling in a second Postgres driver.
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationSource returns the golang-migrate source backed by the embedded
// migrations directory. It requires no database connection, which is what
// lets TestMigrationsParse exercise it without spinning up Postgres.
func migrationSource() (source.Driver, error) {
	return iofs.New(migrationsFS, "migrations")
}

// Migrate applies all pending migrations to databaseURL and returns nil if
// the schema was already up to date (migrate.ErrNoChange). golang-migrate's
// Postgres driver takes a session-level advisory lock for the duration of
// the run, so concurrent boots (e.g. multiple instances starting at once)
// serialize instead of racing each other.
func Migrate(databaseURL string) error {
	src, err := migrationSource()
	if err != nil {
		return fmt.Errorf("store: load migration source: %w", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		_ = src.Close()
		return fmt.Errorf("store: open database/sql handle: %w", err)
	}

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		_ = src.Close()
		_ = db.Close()
		return fmt.Errorf("store: init migrate database driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = src.Close()
		_ = driver.Close()
		return fmt.Errorf("store: init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("store: migrate up: %w", err)
	}
	return nil
}
