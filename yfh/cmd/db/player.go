package db

import (
	"github.com/spf13/cobra"
)

func init() {
	var playerCmd = &cobra.Command{
		Use:   "player",
		Short: "foo",
		Long:  `foo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			/*
				config := core.GetConfig()
				cache, err := core.NewCache(config)
				if err != nil {
					return err
				}
			*/
			return nil
		},
	}
	DBCmd.AddCommand(playerCmd)
}
