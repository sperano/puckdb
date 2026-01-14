package cmd

import (
	"github.com/spf13/cobra"
)

func cmdYahoo() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "yahoo",
		Short: "Yahoo OAuth commands",
		Long:  `Commands for managing Yahoo OAuth2 authentication`,
	}
	cmd.AddCommand(cmdToken(), cmdSignout())
	return cmd
}
