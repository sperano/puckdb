package read

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
)

func init() {
	ReadCmd.AddCommand(rosterCmd)
}

func GetDate(config *core.Config, arg string) (time.Time, error) {
	tokens := strings.Split(arg, "-")
	if len(tokens) != 2 {
		return time.Time{}, errors.New("expected MM-DD")
	}
	monthNo, err := strconv.Atoi(tokens[0])
	if err != nil {
		return time.Time{}, err
	}
	if monthNo < 1 || monthNo > 12 {
		return time.Time{}, fmt.Errorf("invalid month: %d", monthNo)
	}
	month := time.Month(monthNo)
	day, err := strconv.Atoi(tokens[0])
	if err != nil {
		return time.Time{}, err
	}
	year := config.SeasonStartYear
	if month < config.SeasonStartMonth {
		year++
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

var rosterCmd = &cobra.Command{
	Use:   "roster",
	Short: "foo",
	Long:  `foo`,
	Args:  cobra.MinimumNArgs(2),

	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		teamID, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}
		if teamID < 0 || teamID > config.TotalTeams {
			return fmt.Errorf("invalid Team.ID: %d", teamID)
		}
		date, err := GetDate(config, args[1])
		if err != nil {
			return err
		}
		data, err := cache.GetRoster(teamID, date)
		if err != nil {
			return err
		}
		_ = data
		return nil
	},
}
