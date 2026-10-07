package database

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// PoolOptions size the pgx pool. The command layer reads them from the
// --postgres-max-* flags; tests and other callers pass them explicitly.
type PoolOptions struct {
	// MaxConns is the pool's maximum size (--postgres-max-open-conns).
	MaxConns int
	// MinConns is the pool's minimum size, which pgx keeps open
	// (--postgres-max-idle-conns, despite the flag's name).
	MinConns int
	// MaxConnLifetime closes connections older than this.
	MaxConnLifetime time.Duration
}

// Config is everything OpenPGXPool needs: where to connect and how large
// the pool may grow.
type Config struct {
	Conn ConnConfig
	Pool PoolOptions
}

// DefaultPoolOptions returns the pool sizing the flags default to.
func DefaultPoolOptions() PoolOptions {
	return PoolOptions{
		MaxConns:        config.DefaultPostgresMaxOpenConns,
		MinConns:        config.DefaultPostgresMaxIdleConns,
		MaxConnLifetime: config.DefaultDBConnMaxLifetime,
	}
}

// ErrInvalidPoolOptions reports pool sizes pgx would reject or silently cap.
var ErrInvalidPoolOptions = errors.New("invalid postgres pool settings")

// Validate rejects a pool that could never hold a connection, a minimum
// above the maximum (pgx would quietly stop at the maximum), and sizes that
// do not fit pgx's int32 fields. Errors name the flags that set the values.
func (o PoolOptions) Validate() error {
	switch {
	case o.MaxConns < 1 || o.MaxConns > math.MaxInt32:
		return fmt.Errorf("%w: --%s must be between 1 and %d, got %d",
			ErrInvalidPoolOptions, config.FlagPostgresMaxOpenConns, math.MaxInt32, o.MaxConns)
	case o.MinConns < 0 || o.MinConns > o.MaxConns:
		return fmt.Errorf("%w: --%s must be between 0 and --%s (%d), got %d",
			ErrInvalidPoolOptions, config.FlagPostgresMaxIdleConns, config.FlagPostgresMaxOpenConns, o.MaxConns, o.MinConns)
	}
	return nil
}

// OpenPGXPool opens a pgx connection pool for use with SQLC
func OpenPGXPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	log.Debug().Str("host", cfg.Conn.Host).Msg("Initializing pgx pool")
	poolConfig, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	return pool, nil
}

// poolConfig builds the pgx pool configuration without connecting.
func poolConfig(cfg Config) (*pgxpool.Config, error) {
	if err := cfg.Pool.Validate(); err != nil {
		return nil, err
	}
	dbURL, err := cfg.Conn.URL()
	if err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}
	poolConfig.MaxConns = int32(cfg.Pool.MaxConns)
	poolConfig.MinConns = int32(cfg.Pool.MinConns)
	poolConfig.MaxConnLifetime = cfg.Pool.MaxConnLifetime
	return poolConfig, nil
}

// NewQueries creates a new SQLC Queries instance from a pgx pool
func NewQueries(pool *pgxpool.Pool) *sqlcdb.Queries {
	return sqlcdb.New(pool)
}

// DropEverything runs all down migrations against conn to drop database
// tables, then drops golang-migrate's bookkeeping table through pool.
func DropEverything(ctx context.Context, pool *pgxpool.Pool, conn ConnConfig) error {
	log.Info().Msg("Dropping all tables")

	// Run SQL down migrations (handles all tables managed by golang-migrate)
	if err := RunSQLMigrationsDown(conn); err != nil {
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
