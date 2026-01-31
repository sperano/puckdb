package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bsm/redislock"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/redis"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdDB() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database operations",
		Long:  `Database management commands: init, drop, provision.`,
	}
	cmd.AddCommand(cmdDBInit(), cmdDBDrop(), cmdDBProvision())
	return cmd
}

func cmdDBInit() *cobra.Command {
	const lockName = "yfh-init"
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize database",
		Long:  `Run database migrations and seed NHL data. Uses Redis lock to prevent concurrent migrations.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			return config.BindPostgresFlags(flags)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			db, err := database.OpenGorm()
			if err != nil {
				return err
			}
			redisClient := redis.NewClient()
			defer func() { _ = redisClient.Close() }()

			locker := redislock.New(redisClient)
			lock, err := locker.Obtain(ctx, lockName, 60*time.Second, nil)
			if err == redislock.ErrNotObtained {
				log.Warn().Msg("Could not obtain a lock, Another process is probably doing the database migration")
				return nil
			} else if err != nil {
				log.Fatal().Err(err)
			}
			defer func() {
				if err := lock.Release(ctx); err != nil {
					log.Error().Msg(err.Error())
				}
			}()
			if err := database.DoMigration(db); err != nil {
				return err
			}

			// Seed NHL data using SQLC
			pool, err := database.OpenPGXPool(ctx)
			if err != nil {
				return err
			}
			defer pool.Close()
			q := database.NewQueries(pool)
			return database.EnsureNHLWithSQLC(ctx, q)
		},
	}
	flags := cmd.Flags()
	config.InitPostgresFlags(flags)
	config.InitRedisFlags(flags)
	return cmd
}

func cmdDBDrop() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drop",
		Short: "Drop database tables",
		Long:  `Drop all database tables. Use with caution.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindPostgresFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := database.OpenGorm()
			if err != nil {
				return err
			}
			return database.DropEverything(db)
		},
	}
	config.InitPostgresFlags(cmd.Flags())
	return cmd
}

const dbProvisionLockName = "puckdb:db-provision"

func cmdDBProvision() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Create database and user on shared PostgreSQL",
		Long: `Provisions the database and user on a shared PostgreSQL server.
Uses provisioner credentials to create the target database and user.
This command is idempotent and uses a Redis lock to prevent concurrent runs.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			if err := config.BindProvisionerFlags(flags); err != nil {
				return err
			}
			return config.BindPostgresFlags(flags)
		},
		RunE: runDBProvision,
	}
	flags := cmd.Flags()
	config.InitRedisFlags(flags)
	config.InitProvisionerFlags(flags)
	config.InitPostgresFlags(flags)
	return cmd
}

func runDBProvision(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	provisionerHost := viper.GetString(config.FlagProvisionerHost)
	provisionerUser := viper.GetString(config.FlagProvisionerUser)
	provisionerPassword := viper.GetString(config.FlagProvisionerPassword)

	if provisionerHost == "" || provisionerUser == "" {
		return fmt.Errorf("provisioner-host and provisioner-user are required")
	}

	targetUser := viper.GetString(config.FlagPostgresUser)
	targetPassword := viper.GetString(config.FlagPostgresPassword)
	targetDB := viper.GetString(config.FlagPostgresDatabase)

	// Acquire Redis lock to prevent concurrent provisioning
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	locker := redislock.New(redisClient)
	lock, err := locker.Obtain(ctx, dbProvisionLockName, config.DefaultDBProvisionLockTTL, nil)
	if err == redislock.ErrNotObtained {
		log.Warn().Msg("Could not obtain lock, another process is probably provisioning")
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer func() {
		if err := lock.Release(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to release lock")
		}
	}()

	log.Info().
		Str("host", provisionerHost).
		Str("user", provisionerUser).
		Str("target_db", targetDB).
		Str("target_user", targetUser).
		Msg("Provisioning database")

	// Connect as provisioner to postgres database
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/postgres",
		provisionerUser, provisionerPassword, provisionerHost, config.DefaultPostgresPort)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect as provisioner: %w", err)
	}
	defer conn.Close(ctx)

	// Create or update role
	if err := ensureRole(ctx, conn, targetUser, targetPassword); err != nil {
		return err
	}

	// Create database if not exists
	if err := ensureDatabase(ctx, conn, targetDB, provisionerUser); err != nil {
		return err
	}

	// Grant privileges on database
	grantSQL := fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE %s TO %s`,
		pgx.Identifier{targetDB}.Sanitize(), pgx.Identifier{targetUser}.Sanitize())
	if _, err := conn.Exec(ctx, grantSQL); err != nil {
		return fmt.Errorf("failed to grant database privileges: %w", err)
	}
	log.Info().Str("database", targetDB).Str("user", targetUser).Msg("Granted database privileges")

	// Close connection to postgres and connect to target database for schema privileges
	conn.Close(ctx)
	connStr = fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		provisionerUser, provisionerPassword, provisionerHost, config.DefaultPostgresPort, targetDB)
	conn, err = pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", targetDB, err)
	}

	grantSchemaSQL := fmt.Sprintf(`GRANT ALL ON SCHEMA public TO %s`,
		pgx.Identifier{targetUser}.Sanitize())
	if _, err := conn.Exec(ctx, grantSchemaSQL); err != nil {
		return fmt.Errorf("failed to grant schema privileges: %w", err)
	}
	log.Info().Str("user", targetUser).Msg("Granted schema privileges")

	log.Info().Msg("Database provisioning complete")
	return nil
}

func ensureRole(ctx context.Context, conn *pgx.Conn, username, password string) error {
	var roleExists bool
	err := conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT FROM pg_roles WHERE rolname = $1)", username).Scan(&roleExists)
	if err != nil {
		return fmt.Errorf("failed to check if role exists: %w", err)
	}

	escapedPassword := strings.ReplaceAll(password, "'", "''")
	sanitizedUser := pgx.Identifier{username}.Sanitize()

	if !roleExists {
		log.Info().Str("role", username).Msg("Creating role")
		createSQL := fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, sanitizedUser, escapedPassword)
		if _, err := conn.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("failed to create role: %w", err)
		}
	} else {
		log.Info().Str("role", username).Msg("Role exists, updating password")
		alterSQL := fmt.Sprintf(`ALTER ROLE %s PASSWORD '%s'`, sanitizedUser, escapedPassword)
		if _, err := conn.Exec(ctx, alterSQL); err != nil {
			return fmt.Errorf("failed to update password: %w", err)
		}
	}
	return nil
}

func ensureDatabase(ctx context.Context, conn *pgx.Conn, dbName, owner string) error {
	var dbExists bool
	err := conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT FROM pg_database WHERE datname = $1)", dbName).Scan(&dbExists)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if !dbExists {
		log.Info().Str("database", dbName).Msg("Creating database")
		createSQL := fmt.Sprintf(`CREATE DATABASE %s OWNER %s`,
			pgx.Identifier{dbName}.Sanitize(), pgx.Identifier{owner}.Sanitize())
		if _, err := conn.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
	} else {
		log.Info().Str("database", dbName).Msg("Database exists")
	}
	return nil
}
