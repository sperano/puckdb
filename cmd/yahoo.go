package cmd

import (
	"github.com/spf13/cobra"
)

func cmdYahoo() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "yahoo",
		Short: "Manage Yahoo OAuth",
		Long:  `Manage Yahoo OAuth2 authentication.`,
	}
	cmd.AddCommand(cmdSignout())
	return cmd
}
