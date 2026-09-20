package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/viper"
)

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
