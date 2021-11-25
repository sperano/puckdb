package cache

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/cmd/common"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

type ColorPalette struct {
	Attr termenv.Color
	Lo   termenv.Color
	Hi   termenv.Color
}

var term termenv.Profile
var colorPaletteTeam ColorPalette
var colorPaletteTeamLogo ColorPalette
var colorPaletteManager ColorPalette
var colorPalettePlayer ColorPalette
var colorPalettePlayerName ColorPalette
var colorPalettePlayerHeadshot ColorPalette
var colorPalettePlayerSelectedPosition ColorPalette

//var all bool

func init() {
	term = termenv.ColorProfile()
	colorPaletteTeam = ColorPalette{term.Color("#aaffff"), term.Color("#003333"), term.Color("#33aaaa")}
	colorPaletteTeamLogo = ColorPalette{term.Color("#773333"), term.Color("#330000"), term.Color("#aa7777")}
	colorPaletteManager = ColorPalette{term.Color("#333377"), term.Color("#000033"), term.Color("#7777aa")}
	colorPalettePlayer = ColorPalette{term.Color("#aaffaa"), term.Color("#003300"), term.Color("#33aa33")}
	colorPalettePlayerName = ColorPalette{term.Color("#11dd99"), term.Color("#11dd99"), term.Color("#22cc77")}
	colorPalettePlayerHeadshot = ColorPalette{term.Color("#dd9911"), term.Color("#aa7733"), term.Color("#cc7722")}
	colorPalettePlayerSelectedPosition = ColorPalette{term.Color("#9911dd"), term.Color("#6633aa"), term.Color("#7722cc")}
	CacheCmd.AddCommand(teamCmd)
}

func getKeyStr(key string, colorPalette *ColorPalette) string {
	str := ""
	for i, token := range strings.Split(key, ".") {
		if i > 0 {
			str += termenv.String(".").Foreground(colorPalette.Lo).String()
		}
		str += termenv.String(token).Foreground(colorPalette.Hi).String()
	}
	return str
}

func getAttrString(indentLevel int, attr string, colorPalette *ColorPalette) string {
	const width = 40
	prefix := strings.Repeat("  ", indentLevel)
	return prefix +
		termenv.String(attr).Foreground(colorPalette.Attr).String() +
		termenv.String(":").Foreground(colorPalette.Lo).String() +
		strings.Repeat(" ", width-len(attr)-1-len(prefix))
}

func printAttrString(indentLevel int, attr string, value string, colorPalette *ColorPalette) {
	fmt.Println(getAttrString(indentLevel, attr, colorPalette) +
		termenv.String(value).Foreground(colorPalette.Hi).String())
}

func printAttrBool(indentLevel int, attr string, value bool, colorPalette *ColorPalette) {
	fmt.Println(getAttrString(indentLevel, attr, colorPalette) +
		termenv.String(fmt.Sprintf("%t", value)).Foreground(colorPalette.Hi).String())
}

func printAttrInt(indentLevel int, attr string, value int, colorPalette *ColorPalette) {
	fmt.Println(getAttrString(indentLevel, attr, colorPalette) +
		termenv.String(fmt.Sprintf("%d", value)).Foreground(colorPalette.Hi).String())
}

func printHeader(indentLevel int, title string, suffix string, colorPalette *ColorPalette) {
	const gradient = "░░▒▒▓▓█ "
	fmt.Println(strings.Repeat("  ", indentLevel) + termenv.String(gradient).Foreground(colorPalette.Lo).String() + termenv.String(title).Foreground(colorPalette.Attr).String() + " " + suffix)
}

func printTeamStandard(team *xmlmodel.Team) {
	//const gradientColor = "#787844"

	//leftP := termenv.String("(").Foreground(term.Color(teamColorLo)).String()
	//rightP := termenv.String(")").Foreground(term.Color(teamColorLo)).String()
	printHeader(0, "Team", getKeyStr(team.Key, &colorPaletteTeam), &colorPaletteTeam)
	printAttrInt(0, "ID", team.ID, &colorPaletteTeam)
	printAttrString(0, "Key", team.Key, &colorPaletteTeam)
	printAttrString(0, "Name", team.Name, &colorPaletteTeam)
	printAttrBool(0, "Is Owned by Current Login", team.IsOwnedByCurrentLogin, &colorPaletteTeam)
	printAttrString(0, "Team Logos", "", &colorPaletteTeam)
	for _, logo := range team.TeamLogos.Slice {
		printHeader(1, "Team Logo", "", &colorPaletteTeamLogo)
		printAttrString(1, "Size", logo.Size, &colorPaletteTeamLogo)
		printAttrString(1, "URL", logo.URL, &colorPaletteTeamLogo)
	}
	printAttrString(0, "URL", team.URL, &colorPaletteTeam)
	printAttrInt(0, "Waiver Priority", team.WaiverPriority, &colorPaletteTeam)
	printAttrInt(0, "Number of Moves", team.NumberOfMoves, &colorPaletteTeam)
	printAttrInt(0, "Number of Trades", team.NumberOfTrades, &colorPaletteTeam)
	printAttrString(0, "League Scoring Type", team.LeagueScoringType, &colorPaletteTeam)
	printAttrInt(0, "Draft Position", team.DraftPosition, &colorPaletteTeam)
	printAttrBool(0, "Has Draft Grade", team.HasDraftGrade, &colorPaletteTeam)
	printAttrString(0, "Managers", "", &colorPaletteTeam)
	for _, manager := range team.Managers.Slice {
		printHeader(1, "Manager", strconv.Itoa(manager.ID), &colorPaletteManager)
		printAttrInt(1, "ID", manager.ID, &colorPaletteManager)
		printAttrString(1, "Nickname", manager.Nickname, &colorPaletteManager)
		printAttrString(1, "GUID", manager.GUID, &colorPaletteManager)
		printAttrBool(1, "Is Current Login", manager.IsCurrentLogin, &colorPaletteManager)
		printAttrString(1, "E-Mail", manager.EMail, &colorPaletteManager)
		printAttrString(1, "Image URL", manager.ImageURL, &colorPaletteManager)
		printAttrInt(1, "Felo Score", manager.FeloScore, &colorPaletteManager)
		printAttrString(1, "Felo Tier", manager.FeloTier, &colorPaletteManager)
	}
	printAttrString(0, "Roster", "", &colorPaletteTeam)
	printAttrString(1, "Coverage Type", team.Roster.CoverageType, &colorPaletteTeam)
	printAttrString(1, "Date", team.Roster.Date, &colorPaletteTeam)
	printAttrBool(1, "Is Editable", team.Roster.IsEditable, &colorPaletteTeam)
	printAttrString(1, "Players", "", &colorPaletteTeam)
	for _, player := range team.Roster.Players.Slice {
		printHeader(2, "Player", getKeyStr(player.Key, &colorPalettePlayer), &colorPalettePlayer)
		printAttrInt(2, "ID", player.ID, &colorPalettePlayer)
		printAttrString(2, "Key", player.Key, &colorPalettePlayer)
		printAttrString(2, "Name", "", &colorPalettePlayer)
		printHeader(3, player.Name.Full, "", &colorPalettePlayerName)
		printAttrString(3, "Full Name", player.Name.Full, &colorPalettePlayerName)
		printAttrString(3, "First Name", player.Name.First, &colorPalettePlayerName)
		printAttrString(3, "Last Name", player.Name.Last, &colorPalettePlayerName)
		printAttrString(3, "ASCII First Name", player.Name.ASCIIFirst, &colorPalettePlayerName)
		printAttrString(3, "ASCII Last Name", player.Name.ASCIILast, &colorPalettePlayerName)
		printAttrString(2, "Editorial Player Key", player.EditorialPlayerKey, &colorPalettePlayer)
		printAttrString(2, "Editorial Team Key", player.EditorialTeamKey, &colorPalettePlayer)
		printAttrString(2, "Editorial Team Full Name", player.EditorialTeamFullName, &colorPalettePlayer)
		printAttrString(2, "Editorial Team Abbr", player.EditorialTeamAbbr, &colorPalettePlayer)
		printAttrInt(2, "Uniform Number", player.UniformNumber, &colorPalettePlayer)
		printAttrString(2, "Display Position", player.DisplayPosition, &colorPalettePlayer)
		printAttrString(2, "Headshot", "", &colorPalettePlayer)
		printAttrString(3, "URL", player.Headshot.URL, &colorPalettePlayerHeadshot)
		printAttrString(3, "Size", player.Headshot.Size, &colorPalettePlayerHeadshot)
		printAttrString(2, "Image URL", player.ImageURL, &colorPalettePlayer)
		printAttrBool(2, "Is Undroppable", player.IsUndroppable, &colorPalettePlayer)
		printAttrString(2, "Position Type", player.PositionType, &colorPalettePlayer)
		printAttrString(2, "Primary Position", player.PrimaryPosition, &colorPalettePlayer)
		printAttrString(2, english.Plural(len(player.EligiblePositions), "Eligible Position", ""), strings.Join(player.EligiblePositions, " "), &colorPalettePlayer)
		printAttrBool(2, "Has Player Notes", player.HasPlayerNotes, &colorPalettePlayer)
		printAttrInt(2, "Player Notes Last Timestamp", player.PlayerNotesLastTimestamp, &colorPalettePlayer)
		printAttrString(2, "Selected Position", "", &colorPalettePlayer)
		printAttrString(3, "Coverage Type", player.SelectedPosition.CoverageType, &colorPalettePlayerSelectedPosition)
		printAttrString(3, "Date", player.SelectedPosition.Date, &colorPalettePlayerSelectedPosition)
		printAttrString(3, "Position", player.SelectedPosition.Position, &colorPalettePlayerSelectedPosition)
		printAttrBool(3, "Is Flex", player.SelectedPosition.IsFlex, &colorPalettePlayerSelectedPosition)
		printAttrBool(2, "Is Editable", player.IsEditable, &colorPalettePlayer)
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
		if _, _, err := common.GetMonthDayPair(args[1]); err != nil {
			return err
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		config := core.GetConfig(common.ConfigPath)
		cache, err := core.NewCache(config)
		if err != nil {
			return err
		}
		teamID, _ := strconv.Atoi(args[0])
		if teamID < 0 || teamID > config.TotalTeams {
			return &ErrInvalidTeam{TeamID: teamID}
		}
		date, _ := common.GetDate(config, args[1])
		team, err := cache.GetRoster(teamID, date)
		if err != nil {
			return err
		}
		printTeamStandard(team)
		return nil
	},
}
