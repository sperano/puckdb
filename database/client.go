package database

import (
	"context"
	"embed"
	"fmt"
	"net/http"

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

func getDSN(host string, user string, password string, dbname string, port int, sslmode string, timezone string) string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s timezone=%s",
		host, user, password, dbname, port, sslmode, timezone)
}

func GetDSN() string {
	return getDSN(viper.GetString(config.FlagPostgresHost),
		viper.GetString(config.FlagPostgresUser),
		viper.GetString(config.FlagPostgresPassword),
		viper.GetString(config.FlagPostgresDatabase),
		viper.GetInt(config.FlagPostgresPort),
		viper.GetString(config.FlagPostgresSSLMode),
		viper.GetString(config.FlagPostgresTimeZone))
}

// GetDatabaseURL returns a PostgreSQL connection URL for pgx
func GetDatabaseURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		viper.GetString(config.FlagPostgresUser),
		viper.GetString(config.FlagPostgresPassword),
		viper.GetString(config.FlagPostgresHost),
		viper.GetInt(config.FlagPostgresPort),
		viper.GetString(config.FlagPostgresDatabase),
		viper.GetString(config.FlagPostgresSSLMode))
}

// OpenPGXPool opens a pgx connection pool for use with SQLC
func OpenPGXPool(ctx context.Context) (*pgxpool.Pool, error) {
	dbURL := GetDatabaseURL()
	log.Info().Str("host", viper.GetString(config.FlagPostgresHost)).Msg("Initializing pgx pool")

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


// RunSQLMigrations runs the embedded SQL migrations
func RunSQLMigrations() error {
	dbURL := GetDatabaseURL()
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

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	version, dirty, _ := m.Version()
	log.Info().Uint("version", version).Bool("dirty", dirty).Msg("SQL migrations completed")
	return nil
}

// RunSQLMigrationsDown runs all down migrations
func RunSQLMigrationsDown() error {
	dbURL := GetDatabaseURL()
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

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
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

const sqlcContextKey = "sqlc"

// SQLCMiddleware adds SQLC queries to the request context
func SQLCMiddleware(next http.Handler) http.Handler {
	pool, err := OpenPGXPool(context.Background())
	if err != nil {
		log.Error().Err(err).Msg("can't open pgx pool")
	}
	queries := sqlcdb.New(pool)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			log.Error().Err(err).Msg("can't open pgx pool")
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		ctx := context.WithValue(r.Context(), sqlcContextKey, queries)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// QueriesFromContext retrieves SQLC queries from context
func QueriesFromContext(ctx context.Context) *sqlcdb.Queries {
	return ctx.Value(sqlcContextKey).(*sqlcdb.Queries)
}
