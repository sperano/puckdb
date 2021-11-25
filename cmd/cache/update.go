package cache

import (
	"fmt"

	"github.com/ericsperano/yfh/cmd/common"
	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

var from string
var to string
var force bool
var fantasyGameExplicit bool
var teamExplicit int

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "updates the local cache",
	Long:  `Updates the local cache`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		dates, err := common.GetDatesFromCommandLine(config, from, to, args)
		if err != nil {
			return err
		}
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		// check if fantasy game data is downloaded

		_ = cache
		for _, d := range dates {
			fmt.Printf("%s\n", d)
		}
		return nil
	},
}

func init() {
	updateCmd.PersistentFlags().StringVarP(&from, "from", "F", "", "Update from this date")
	updateCmd.PersistentFlags().StringVarP(&to, "to", "T", "", "Update until and excluding this date")
	updateCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Force download")
	updateCmd.PersistentFlags().BoolVarP(&fantasyGameExplicit, "fantasy-game", "g", false, "Only fantasy game data")
	CacheCmd.AddCommand(updateCmd)
}
