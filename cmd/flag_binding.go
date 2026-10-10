package cmd

import (
	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
)

// bindFlagsPreRunE returns a cobra PreRunE that binds the given flag groups
// from the command's local flag set to viper, returning the first binding
// error. Commands use it with the same groups they registered with
// config.InitFlags so registration and binding cannot drift.
func bindFlagsPreRunE(groups ...*config.FlagGroup) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return config.BindFlags(cmd.Flags(), groups...)
	}
}
