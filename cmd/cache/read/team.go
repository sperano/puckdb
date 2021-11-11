package read

import (
	"fmt"
	"strconv"

	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

func init() {
	ReadCmd.AddCommand(teamCmd)
}

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "foo",
	Long:  `foo`,
	Args:  cobra.MinimumNArgs(1),

	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		for _, arg := range args {
			teamID, err := strconv.Atoi(arg)
			if err != nil {
				return err
			}
			if teamID < 0 || teamID > config.TotalTeams {
				return fmt.Errorf("invalid Team.ID: %d", teamID)
			}
			team, err := cache.GetTeam(teamID)
			if err != nil {
				return err
			}
			fmt.Print("=== Team ===\n")
			fmt.Printf("ID:                    %d\n", team.ID)
			fmt.Printf("Key:                   %s\n", team.Key)
		}
		return nil
	},
}
