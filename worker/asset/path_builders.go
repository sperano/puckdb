package asset

import (
	"fmt"

	"github.com/sperano/puckdb/core"
)

// Expected ID arity per FileType. Using named constants makes arity
// mismatches self-documenting in error messages.
const (
	arityPlayer       = 1 // playerID
	arityTeam         = 1 // teamID
	arityLeague       = 1 // leagueID
	arityYahooTeam    = 2 // leagueID, teamID
	arityYahooManager = 3 // leagueID, teamID, managerID
)

// pathBuilders maps each asset FileType to a function that constructs its
// cache path given the IDs and URL-derived file extension.
var pathBuilders = map[core.FileType]func(ids []int64, ext string) (string, error){
	core.PlayerHeadshot: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityPlayer {
			return "", fmt.Errorf("PlayerHeadshot expects %d ID(s), got %d", arityPlayer, len(ids))
		}
		return fmt.Sprintf("assets/players/%d/headshot.%s", ids[0], ext), nil
	},
	core.PlayerHeroImage: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityPlayer {
			return "", fmt.Errorf("PlayerHeroImage expects %d ID(s), got %d", arityPlayer, len(ids))
		}
		return fmt.Sprintf("assets/players/%d/hero.%s", ids[0], ext), nil
	},
	core.PlayerYahooImageSmall: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityPlayer {
			return "", fmt.Errorf("PlayerYahooImageSmall expects %d ID(s), got %d", arityPlayer, len(ids))
		}
		return fmt.Sprintf("assets/players/%d/yahoo-small.%s", ids[0], ext), nil
	},
	core.PlayerYahooImageMedium: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityPlayer {
			return "", fmt.Errorf("PlayerYahooImageMedium expects %d ID(s), got %d", arityPlayer, len(ids))
		}
		return fmt.Sprintf("assets/players/%d/yahoo-medium.%s", ids[0], ext), nil
	},
	core.PlayerYahooImageLarge: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityPlayer {
			return "", fmt.Errorf("PlayerYahooImageLarge expects %d ID(s), got %d", arityPlayer, len(ids))
		}
		return fmt.Sprintf("assets/players/%d/yahoo-large.%s", ids[0], ext), nil
	},
	core.TeamLogo: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityTeam {
			return "", fmt.Errorf("TeamLogo expects %d ID(s), got %d", arityTeam, len(ids))
		}
		return fmt.Sprintf("assets/teams/logos/%d.%s", ids[0], ext), nil
	},
	core.YahooTeamLogo: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityYahooTeam {
			return "", fmt.Errorf("YahooTeamLogo expects %d ID(s), got %d", arityYahooTeam, len(ids))
		}
		return fmt.Sprintf("assets/yahoo/leagues/%d/teams/%d/logo.%s", ids[0], ids[1], ext), nil
	},
	core.YahooLeagueLogo: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityLeague {
			return "", fmt.Errorf("YahooLeagueLogo expects %d ID(s), got %d", arityLeague, len(ids))
		}
		return fmt.Sprintf("assets/yahoo/leagues/%d/league-logo.%s", ids[0], ext), nil
	},
	core.YahooManagerImage: func(ids []int64, ext string) (string, error) {
		if len(ids) != arityYahooManager {
			return "", fmt.Errorf("YahooManagerImage expects %d ID(s), got %d", arityYahooManager, len(ids))
		}
		return fmt.Sprintf("assets/yahoo/leagues/%d/teams/%d/manager-%d.%s", ids[0], ids[1], ids[2], ext), nil
	},
}
