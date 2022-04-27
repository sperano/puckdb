package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "yfh",
	Short: "Yahoo Fantasy Hockey",
	Long:  "TODO",
}

func Execute() error {
	return rootCmd.Execute()
}
