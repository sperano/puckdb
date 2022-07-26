package worker

import (
	"context"
	"fmt"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func HandleImportTeams(ctx context.Context, yfh *core.YFH) error {
	log.Debug().Msg("HandleImportTeams")
	for _, teamID := range core.GetTeamIDs() {
		if err := HandleImportTeam(ctx, yfh, teamID); err != nil {
			return err
		}
	}
	return nil
}

func EnsureRecentTeam(ctx context.Context, yfh *core.YFH, teamID uint) (*core.LocalFile, error) {
	files, err := yfh.Local.FindTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Found %s", english.Plural(len(files), "team file", ""))
	if len(files) == 0 {
		content, err := Download(ctx, yfh, core.YahooTeamURL(viper.GetInt(core.FlagLeagueID), teamID))
		if err != nil {
			return nil, err
		}
		if err := yfh.Local.CreateTeam(ctx, teamID, content); err != nil {
			return nil, err
		}
		files, err = yfh.Local.FindTeam(ctx, teamID)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("should have found at least one file for team %d", teamID)
		}
	}
	return files[0], nil
}

func HandleImportTeam(ctx context.Context, yfh *core.YFH, teamID uint) error {
	log.Debug().Uint("teamID", teamID).Msg("HandleImportTeam")
	file, err := EnsureRecentTeam(ctx, yfh, teamID)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Local.ParseXML(ctx, path.Join(core.GetTeamDir(teamID), file.Filename()))
	if err != nil {
		return err
	}
	team := fantasy.Team.ToTeamModel()
	team.GithubTimestamp = file.Time
	log.Debug().Uint("teamID", team.ID).Msg("Ensuring team")
	return team.Ensure(yfh.GormDB)
}
