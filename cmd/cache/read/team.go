package read

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/spf13/cobra"
)

func init() {
	ReadCmd.AddCommand(teamCmd)
}

func printTeam(team *xmlmodel.Team) {
	fmt.Printf("=== Team %s (%d) ===\n", team.Key, team.ID)
	fmt.Printf("Name:                      %s\n", team.Name)
	fmt.Printf("Is Owned by Current Login: %v\n", team.IsOwnedByCurrentLogin)
	fmt.Printf("Waiver Priority:           %d\n", team.WaiverPriority)
	fmt.Printf("Number of Moves:           %d\n", team.NumberOfMoves)
	fmt.Printf("Number of Trades:          %d\n", team.NumberOfTrades)
	fmt.Printf("URL:                       %s\n", team.URL)
	fmt.Printf("Team Logos:\n")
	for _, logo := range team.TeamLogos.Slice {
		fmt.Printf("  - Size: %s, URL: %s\n", logo.Size, logo.URL)
	}
	//TeamLogos      TeamLogos
	fmt.Printf("Roster:\n")
	fmt.Printf("  Coverage Type:           %s\n", team.Roster.CoverageType)
	fmt.Printf("  Date:                    %s\n", team.Roster.Date)
	fmt.Printf("  Is Editable:             %v\n", team.Roster.IsEditable)
	fmt.Printf("  Players:                 %d\n", team.Roster.Players.Count)
	for _, player := range team.Roster.Players.Slice {
		fmt.Printf("  === Player %s (%d) ===\n", player.PlayerKey, player.PlayerID)
		fmt.Printf("  Full Name:               %s\n", player.Name.Full)
		fmt.Printf("  First Name:              %s\n", player.Name.First)
		fmt.Printf("  Last Name:               %s\n", player.Name.Last)
		fmt.Printf("  ASCII First Name:        %s\n", player.Name.ASCIIFirst)
		fmt.Printf("  ASCII Last Name:         %s\n", player.Name.ASCIILast)
	}
}

type ErrInvalidTeam struct {
	TeamID int
}

func (e *ErrInvalidTeam) Error() string {
	return fmt.Sprintf("invalid team_id: %d", e.TeamID)
}

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "foo",
	Long:  `foo`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			// TODO const for yfh
			return errors.New("expected two arguments: <Team ID> <MM-DD>")
		}
		if _, err := strconv.Atoi(args[0]); err != nil {
			return err
		}
		if _, _, err := GetMonthDayPair(args[1]); err != nil {
			return err
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig()
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		teamID, _ := strconv.Atoi(args[0])
		if teamID < 0 || teamID > config.TotalTeams {
			return &ErrInvalidTeam{TeamID: teamID}
		}
		date, _ := GetDate(config, args[1])
		team, err := cache.GetTeamRoster(teamID, date)
		if err != nil {
			return err
		}
		printTeam(team)
		return nil
	},
}

func GetMonthDayPair(arg string) (time.Month, int, error) {
	tokens := strings.Split(arg, "-")
	if len(tokens) != 2 {
		return 0, 0, errors.New("expected MM-DD")
	}
	monthNo, err := strconv.Atoi(tokens[0])
	if err != nil {
		return 0, 0, err
	}
	if monthNo < 1 || monthNo > 12 {
		return 0, 0, fmt.Errorf("invalid month: %d", monthNo)
	}
	month := time.Month(monthNo)
	day, err := strconv.Atoi(tokens[1])
	if err != nil {
		return month, 0, err
	}
	return month, day, nil
}

func GetDate(config *core.Config, arg string) (time.Time, error) {
	month, day, err := GetMonthDayPair(arg)
	if err != nil {
		return time.Time{}, err
	}
	year := config.SeasonStartYear
	if month < config.SeasonStartMonth {
		year++
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}
