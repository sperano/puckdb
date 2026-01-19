package cmd

import (
	"context"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/redis"
	"time"

	"github.com/bsm/redislock"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func cmdInit() *cobra.Command {
	const lockName = "yfh-init"
	var cmd = &cobra.Command{
		Use:   "init",
		Short: "Initialize database",
		Long:  `Run database migrations and seed NHL data. Uses Redis lock to prevent concurrent migrations.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			if err := config.BindPostgresFlags(flags); err != nil {
				return err
			}
			return nil
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
