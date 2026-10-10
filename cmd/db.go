package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/database"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func getGraphQLClient() (*GraphQLClient, error) {
	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return nil, fmt.Errorf("api-server-addr is required")
	}
	return NewGraphQLClient(apiAddr), nil
}

func cmdDB() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database operations",
		Long:  `Database management commands: init, drop, provision, migrate, force-version, check-teams.`,
	}
	cmd.AddCommand(cmdDBInit(), cmdDBDrop(), cmdDBProvision(), cmdDBMigrate(), cmdDBForceVersion(), cmdDBCheckTeams())
	return cmd
}

const dbInitLockName = "puckdb:db-init"
const dbMigrateLockName = "puckdb:db-migrate"

// dbMigrateFlagGroups lists every flag group `db migrate` exposes. Defined
// once and shared by InitFlags and BindFlags so the two can never drift.
var dbMigrateFlagGroups = []*config.FlagGroup{
	&config.RedisFlags,
	&config.PostgresFlags,
}

func cmdDBMigrate() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "migrate",
		Short:   "Run database migrations directly",
		Long:    `Run database migrations directly without going through GraphQL API. Designed for init containers.`,
		PreRunE: bindFlagsPreRunE(dbMigrateFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRedisLock(cmd.Context(), dbMigrateLockName, config.DefaultDBInitLockTTL, func() error {
				if err := database.DoMigration(postgresConnFrom(viper.GetViper())); err != nil {
					return fmt.Errorf("migration failed: %w", err)
				}
				log.Info().Msg("Database migrations completed successfully")
				return nil
			})
		},
	}
	config.InitFlags(cmd.Flags(), dbMigrateFlagGroups...)
	return cmd
}

func cmdDBForceVersion() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "force-version <version>",
		Short: "Mark a dirty migration state clean at a version, without running SQL",
		Long: `Repair a database left dirty by a failed migration.

This runs no migration SQL: it only records that the schema matches <version>.
Inspect the schema first and pass the version it actually matches; use 0 when
no migration is applied. Refuses to act on a database that is not dirty, and on
a version the embedded migrations do not define. See https://wiki.spe.quebec/en/puckdb/migration-recovery.`,
		Args:    cobra.ExactArgs(1),
		PreRunE: bindFlagsPreRunE(dbMigrateFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("version must be an integer: %w", err)
			}
			return withRedisLock(cmd.Context(), dbMigrateLockName, config.DefaultDBInitLockTTL, func() error {
				if err := database.ForceMigrationVersion(postgresConnFrom(viper.GetViper()), version); err != nil {
					return fmt.Errorf("force version failed: %w", err)
				}
				log.Info().Int("version", version).Msg("Migration version forced; run `db migrate` next")
				return nil
			})
		},
	}
	config.InitFlags(cmd.Flags(), dbMigrateFlagGroups...)
	return cmd
}

// dbInitFlagGroups lists every flag group `db init` exposes. Defined once
// and shared by InitFlags and BindFlags so the two can never drift.
var dbInitFlagGroups = []*config.FlagGroup{
	&config.RedisFlags,
	&config.AdminAuthFlags,
	&config.APIBasicAuthFlags,
}

func cmdDBInit() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Initialize database",
		Long:    `Run database migrations via GraphQL API. Uses Redis lock to prevent concurrent migrations.`,
		PreRunE: bindFlagsPreRunE(dbInitFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return withRedisLock(ctx, dbInitLockName, config.DefaultDBInitLockTTL, func() error {
				success, err := client.CreateDatabase(ctx)
				if err != nil {
					return fmt.Errorf("initialize database: %w", err)
				}
				if success {
					log.Info().Msg("Database initialized successfully")
				}
				return nil
			})
		},
	}
	config.InitFlags(cmd.Flags(), dbInitFlagGroups...)
	return cmd
}

func cmdDBDrop() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "drop",
		Short:   "Drop database tables",
		Long:    `Drop all database tables via GraphQL API. Use with caution.`,
		PreRunE: bindFlagsPreRunE(&config.AdminAuthFlags, &config.APIBasicAuthFlags),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			success, err := client.DropDatabase(cmd.Context())
			if err != nil {
				return fmt.Errorf("drop database: %w", err)
			}
			if success {
				log.Info().Msg("Database tables dropped successfully")
			}
			return nil
		},
	}
	config.InitFlags(cmd.Flags(), &config.AdminAuthFlags, &config.APIBasicAuthFlags)
	return cmd
}

const dbProvisionLockName = "puckdb:db-provision"

// dbProvisionFlagGroups lists every flag group `db provision` exposes.
// Defined once and shared by InitFlags and BindFlags so the two can never drift.
var dbProvisionFlagGroups = []*config.FlagGroup{
	&config.RedisFlags,
	&config.ProvisionerFlags,
	&config.PostgresFlags,
}

func cmdDBProvision() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Create database and user on shared PostgreSQL",
		Long: `Provisions the database and user on a shared PostgreSQL server.
Uses provisioner credentials to create the target database and user.
This command is idempotent and uses a Redis lock to prevent concurrent runs.`,
		PreRunE: bindFlagsPreRunE(dbProvisionFlagGroups...),
		RunE:    runDBProvision,
	}
	config.InitFlags(cmd.Flags(), dbProvisionFlagGroups...)
	return cmd
}

func runDBProvision(cmd *cobra.Command, args []string) error {
	provisionerHost := viper.GetString(config.FlagProvisionerHost)
	provisionerUser := viper.GetString(config.FlagProvisionerUser)
	provisionerPassword := viper.GetString(config.FlagProvisionerPassword)

	if provisionerHost == "" || provisionerUser == "" {
		return fmt.Errorf("provisioner-host and provisioner-user are required")
	}

	targetUser := viper.GetString(config.FlagPostgresUser)
	targetPassword := viper.GetString(config.FlagPostgresPassword)
	targetDB := viper.GetString(config.FlagPostgresDatabase)
	ctx := cmd.Context()

	return withRedisLock(ctx, dbProvisionLockName, config.DefaultDBProvisionLockTTL, func() error {
		log.Info().
			Str("host", provisionerHost).
			Str("user", provisionerUser).
			Str("target_db", targetDB).
			Str("target_user", targetUser).
			Msg("Provisioning database")

		connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/postgres",
			provisionerUser, provisionerPassword, provisionerHost, config.DefaultPostgresPort)

		conn, err := pgx.Connect(ctx, connStr)
		if err != nil {
			return fmt.Errorf("connect as provisioner: %w", err)
		}
		defer conn.Close(ctx)

		if err := ensureRole(ctx, conn, targetUser, targetPassword); err != nil {
			return err
		}

		if err := ensureDatabase(ctx, conn, targetDB, provisionerUser); err != nil {
			return err
		}

		grantSQL := fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE %s TO %s`,
			pgx.Identifier{targetDB}.Sanitize(), pgx.Identifier{targetUser}.Sanitize())
		if _, err := conn.Exec(ctx, grantSQL); err != nil {
			return fmt.Errorf("grant database privileges: %w", err)
		}
		log.Info().Str("database", targetDB).Str("user", targetUser).Msg("Granted database privileges")

		conn.Close(ctx)
		connStr = fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
			provisionerUser, provisionerPassword, provisionerHost, config.DefaultPostgresPort, targetDB)
		conn, err = pgx.Connect(ctx, connStr)
		if err != nil {
			return fmt.Errorf("connect to %s: %w", targetDB, err)
		}

		grantSchemaSQL := fmt.Sprintf(`GRANT ALL ON SCHEMA public TO %s`,
			pgx.Identifier{targetUser}.Sanitize())
		if _, err := conn.Exec(ctx, grantSchemaSQL); err != nil {
			return fmt.Errorf("grant schema privileges: %w", err)
		}
		log.Info().Str("user", targetUser).Msg("Granted schema privileges")

		if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
			log.Warn().Err(err).Msg("Could not create pg_stat_statements extension (may require superuser)")
		} else {
			log.Info().Msg("pg_stat_statements extension enabled")
		}

		log.Info().Msg("Database provisioning complete")
		return nil
	})
}

func ensureRole(ctx context.Context, conn *pgx.Conn, username, password string) error {
	var roleExists bool
	err := conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT FROM pg_roles WHERE rolname = $1)", username).Scan(&roleExists)
	if err != nil {
		return fmt.Errorf("check if role exists: %w", err)
	}

	escapedPassword := strings.ReplaceAll(password, "'", "''")
	sanitizedUser := pgx.Identifier{username}.Sanitize()

	if !roleExists {
		log.Info().Str("role", username).Msg("Creating role")
		createSQL := fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, sanitizedUser, escapedPassword)
		if _, err := conn.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("create role: %w", err)
		}
	} else {
		// Role exists - try to update password but don't fail if we lack permission
		// (provisioner may not own this role if it was created by a superuser)
		log.Info().Str("role", username).Msg("Role exists, attempting password update")
		alterSQL := fmt.Sprintf(`ALTER ROLE %s PASSWORD '%s'`, sanitizedUser, escapedPassword)
		if _, err := conn.Exec(ctx, alterSQL); err != nil {
			log.Warn().Err(err).Str("role", username).Msg("Could not update password (may lack permission)")
		}
	}
	return nil
}

func ensureDatabase(ctx context.Context, conn *pgx.Conn, dbName, owner string) error {
	var dbExists bool
	err := conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT FROM pg_database WHERE datname = $1)", dbName).Scan(&dbExists)
	if err != nil {
		return fmt.Errorf("check if database exists: %w", err)
	}

	if !dbExists {
		log.Info().Str("database", dbName).Msg("Creating database")
		createSQL := fmt.Sprintf(`CREATE DATABASE %s OWNER %s`,
			pgx.Identifier{dbName}.Sanitize(), pgx.Identifier{owner}.Sanitize())
		if _, err := conn.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("create database: %w", err)
		}
	} else {
		log.Info().Str("database", dbName).Msg("Database exists")
	}
	return nil
}
