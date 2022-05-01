package worker

import (
	"context"
	"fmt"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func HandleImportTeams(ctx context.Context, yfh *core.YFH) error {
	for _, teamID := range core.GetTeamIDs() {
		if err := HandleImportTeam(ctx, yfh, teamID); err != nil {
			return err
		}
	}
	return nil
}

func EnsureRecentTeam(ctx context.Context, yfh *core.YFH, teamID uint) (*core.GithubFile, error) {
	gitfiles, err := yfh.Github.FindTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s in github", english.Plural(len(gitfiles), "team file", ""))
	if len(gitfiles) == 0 {
		content, err := Download(ctx, yfh, core.YahooTeamURL(viper.GetInt(core.FlagLeagueID), teamID))
		if err != nil {
			return nil, err
		}
		if err := yfh.Github.CreateTeam(ctx, teamID, content); err != nil {
			return nil, err
		}
		gitfiles, err = yfh.Github.FindTeam(ctx, teamID)
		if err != nil {
			return nil, err
		}
		if len(gitfiles) == 0 {
			return nil, fmt.Errorf("should have found at least one file for team %d", teamID)
		}
	}
	return gitfiles[0], nil
}

func HandleImportTeam(ctx context.Context, yfh *core.YFH, teamID uint) error {
	// check if the team is in the github cache first
	gitfile, err := EnsureRecentTeam(ctx, yfh, teamID)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.GetTeamDir(teamID), gitfile.Filename()))
	if err != nil {
		return err
	}
	team := fantasy.Team.ToTeamModel()
	team.GithubTimestamp = gitfile.Time
	log.Debugf("Ensuring team: %02d", team.ID)
	return team.Ensure(yfh.GormDB)
}
