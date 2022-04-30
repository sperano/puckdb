package worker

import (
	"context"
	"errors"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func EnsureRecentLeague(ctx context.Context, yfh *core.YFH) (*core.GithubFile, error) {
	gitfiles, err := yfh.Github.FindLeague(ctx)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s in github", english.Plural(len(gitfiles), "fantasy game file", ""))
	if len(gitfiles) == 0 {
		content, err := Download(ctx, yfh, core.YahooLeagueURL(viper.GetInt(core.FlagLeagueID)))
		if err != nil {
			return nil, err
		}
		if err := yfh.Github.CreateLeague(ctx, content); err != nil {
			return nil, err
		}
		gitfiles, err = yfh.Github.FindLeague(ctx)
		if err != nil {
			return nil, err
		}
		if len(gitfiles) == 0 {
			return nil, errors.New("should have found at least one file for fantasy games")
		}
	}
	return gitfiles[0], nil
}

func HandleImportLeague(ctx context.Context, yfh *core.YFH) error {
	var count int64
	yfh.GormDB.Model(&model.League{}).Count(&count)
	log.Debugf("Count: %d", count)
	if count > 0 {
		log.Infof("Already has %s in the database", english.Plural(int(count), "league", ""))
		return nil
	}
	log.Info("No league found in the database")
	// check if the game list is in the github cache first
	gitfile, err := EnsureRecentLeague(ctx, yfh)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.LeagueDirectory, gitfile.Filename()))
	if err != nil {
		return err
	}
	obj, err := fantasy.League.ToLeagueModel()
	if err != nil {
		return err
	}
	obj.GithubTimestamp = gitfile.Time
	log.Info("Creating fantasy game in database")
	return yfh.GormDB.Create(&obj).Error
}
