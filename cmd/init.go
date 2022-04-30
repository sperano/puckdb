package cmd

import (
	"context"
	"time"

	"github.com/bsm/redislock"
	"github.com/ericsperano/yfh/core"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func init() {
	const lockName = "yfh-init-container"
	var initCmd = &cobra.Command{
		Use:   "init",
		Short: "Do the migration and other init container duties",
		Long:  `Do the migration and other init container duties`,
		RunE: func(cmd *cobra.Command, args []string) error {
			core.SetLogLevel()
			core.LogIntro()

			yfh, err := core.NewYFH()
			if err != nil {
				return err
			}
			defer yfh.Close()

			locker := redislock.New(yfh.RedisClient)
			ctx := context.Background()
			lock, err := locker.Obtain(ctx, lockName, 60*time.Second, nil)
			if err == redislock.ErrNotObtained {
				log.Warn("Could not obtain a lock, Another process is probably doing the database migration")
				return nil
			} else if err != nil {
				log.Fatalln(err)
			}
			defer lock.Release(ctx)
			orm, err := core.OpenGorm()
			if err != nil {
				return err
			}
			return core.DoMigration(orm)
		},
	}
	flags := initCmd.Flags()
	core.SetupViperLogLevel(flags)
	core.SetupViperPostgres(flags)
	rootCmd.AddCommand(initCmd)
}
