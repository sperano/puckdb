package cmd

import (
	"os"

	"github.com/ericsperano/yfh/cmd/cache"
	"github.com/ericsperano/yfh/cmd/db"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "yfh",
	Short: "Yahoo Fantasy Hockey Cloner",
	Long:  "",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(cache.CacheCmd)
	rootCmd.AddCommand(db.DBCmd)
}
