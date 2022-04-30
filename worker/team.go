package worker

import (
	"context"
	"fmt"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func HandleImportTeams(ctx context.Context, yfh *core.YFH) error {
	for _, teamID := range core.GetTeamIds() {
		if err := HandleImportTeam(ctx, yfh, teamID); err != nil {
			return err
		}
	}
	return nil
}

func EnsureRecentTeam(ctx context.Context, yfh *core.YFH, teamID int) (*core.GithubFile, error) {
	gitfiles, err := yfh.Github.FindTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s in github", english.Plural(len(gitfiles), "fantasy game file", ""))
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

func HandleImportTeam(ctx context.Context, yfh *core.YFH, teamID int) error {
	var count int64
	if err := yfh.GormDB.Find(&model.Team{}, "id=?", teamID).Count(&count).Error; err != nil {
		return err
	}
	log.Debugf("Count: %d", count)
	if count > 0 {
		log.Infof("Already has %s in the database for id %d", english.Plural(int(count), "team", ""), teamID)
		return nil
	}
	log.Infof("No team found in the database for id %d", teamID)
	// check if the game list is in the github cache first
	gitfile, err := EnsureRecentTeam(ctx, yfh, teamID)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.GetTeamDir(teamID), gitfile.Filename()))
	if err != nil {
		return err
	}
	obj := fantasy.Team.ToTeamModel()
	obj.GithubTimestamp = gitfile.Time
	log.Infof("Creating team in database for id %d", teamID)
	return yfh.GormDB.Create(&obj).Error
}
