package cmd

import (
	"fmt"
	"os"

	"github.com/ericsperano/yfh/cmd/cache"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "yfh",
	Short: "Yahoo Fantasy Hockey Cloner",
	Long: `A Fast and Flexible Static Site Generator built with
				  love by spf13 and friends in Go.
				  Complete documentation is available at http://hugo.spf13.com`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(cache.CacheCmd)
}
