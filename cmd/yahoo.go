package cmd

import (
	"github.com/spf13/cobra"
)

func cmdYahoo() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "yahoo",
		Short: "Manage Yahoo OAuth and API access",
		Long:  `Manage Yahoo OAuth2 authentication and check Yahoo API access.`,
	}
	cmd.AddCommand(cmdSignout(), cmdYahooCheckAccess())
	return cmd
}
