package cmd

import (
	"fmt"
	"os"
	"path"

	"github.com/ericsperano/yfh/cmd/cache"
	"github.com/ericsperano/yfh/cmd/common"
	"github.com/ericsperano/yfh/cmd/db"
	"github.com/ericsperano/yfh/core"

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

	home := os.Getenv("HOME")
	rootCmd.PersistentFlags().StringVarP(&common.ConfigPath, "config", "c", path.Join(home, "yfh.yaml"), "Config file path")

	var configCmd = &cobra.Command{
		Use:   "config",
		Short: "prints the config",
		Long:  `Prints the config`,
		Run: func(cmd *cobra.Command, args []string) {
			config := core.GetConfig(common.ConfigPath)
			fmt.Printf("%+v\n", config)
		},
	}
	rootCmd.AddCommand(configCmd)
}
