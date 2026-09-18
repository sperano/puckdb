package database

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/spf13/viper"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// OpenPGXPool opens a pgx connection pool for use with SQLC
func OpenPGXPool(ctx context.Context) (*pgxpool.Pool, error) {
	connConfig := ConnConfigFromViper()
	log.Debug().Str("host", connConfig.Host).Msg("Initializing pgx pool")

	dbURL, err := connConfig.URL()
	if err != nil {
		return nil, err
	}

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}

	poolConfig.MaxConns = int32(viper.GetInt(config.FlagPostgresMaxOpenConns))
	poolConfig.MinConns = int32(viper.GetInt(config.FlagPostgresMaxIdleConns))
	poolConfig.MaxConnLifetime = config.DefaultDBConnMaxLifetime

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	return pool, nil
}

// NewQueries creates a new SQLC Queries instance from a pgx pool
func NewQueries(pool *pgxpool.Pool) *sqlcdb.Queries {
	return sqlcdb.New(pool)
}

// MigrateUp runs the embedded SQL migrations against the given
// dbURL. Exposed for integration tests that don't go through viper
// — they configure their connection directly via env vars rather
// than via the puckdb global config flags.
//
// Functionally identical to RunSQLMigrations, just with the URL
// passed in.
func MigrateUp(dbURL string) error {
	return runMigrationsUpAt(dbURL)
}

// MigrateDown is the integration-test counterpart to MigrateUp —
// runs every down migration against dbURL. Tests call this in
// t.Cleanup so a fresh schema lands on every test run.
func MigrateDown(dbURL string) error {
	return runMigrationsDownAt(dbURL)
}

// RunSQLMigrations runs the embedded SQL migrations
func RunSQLMigrations() error {
	dbURL, err := ConnConfigFromViper().URL()
	if err != nil {
		return err
	}
	return runMigrationsUpAt(dbURL)
}

func runMigrationsUpAt(dbURL string) error {
	log.Info().Msg("Running SQL migrations")

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer m.Close()

	// If previous run left a dirty state, force back to prior clean version and retry.
	version, dirty, _ := m.Version()
	if dirty {
		prev := int(version) - 1
		log.Warn().Uint("version", version).Int("force_to", prev).
			Msg("Dirty migration detected — forcing version back to retry")
		if err := m.Force(prev); err != nil {
			return fmt.Errorf("force migration version to %d: %w", prev, err)
		}
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}

	version, dirty, _ = m.Version()
	log.Info().Uint("version", version).Bool("dirty", dirty).Msg("SQL migrations completed")
	return nil
}

// RunSQLMigrationsDown runs all down migrations
func RunSQLMigrationsDown() error {
	dbURL, err := ConnConfigFromViper().URL()
	if err != nil {
		return err
	}
	return runMigrationsDownAt(dbURL)
}

func runMigrationsDownAt(dbURL string) error {
	log.Info().Msg("Running SQL down migrations")

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run down migrations: %w", err)
	}

	log.Info().Msg("SQL down migrations completed")
	return nil
}

func DoMigration() error {
	log.Info().Msg("Starting database migration")
	// Run SQL migrations (handles franchises, seasons, season_teams, players, games, stats)
	if err := RunSQLMigrations(); err != nil {
		return err
	}
	log.Info().Msg("Database migration completed")
	return nil
}

// DropEverything runs all down migrations to drop database tables
func DropEverything(ctx context.Context, pool *pgxpool.Pool) error {
	log.Info().Msg("Dropping all tables")

	// Run SQL down migrations (handles all tables managed by golang-migrate)
	if err := RunSQLMigrationsDown(); err != nil {
		return err
	}

	// Drop the schema_migrations table used by golang-migrate
	_, err := pool.Exec(ctx, "DROP TABLE IF EXISTS schema_migrations")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to drop schema_migrations table")
	}

	log.Info().Msg("All tables dropped")
	return nil
}
