package cmd

import (
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/spf13/cobra"
)

func cmdDrop() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "drop",
		Short: "Drop database tables",
		Long:  `Drop all database tables. Use with caution.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindPostgresFlags(flags); err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := database.OpenGorm()
			if err != nil {
				return err
			}
			return database.DropEverything(db)
		},
	}
	flags := cmd.Flags()
	config.InitPostgresFlags(flags)
	return cmd
}
