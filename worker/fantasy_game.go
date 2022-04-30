package worker

import (
	"context"
	"errors"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	log "github.com/sirupsen/logrus"
)

func EnsureRecentFantasyGame(ctx context.Context, yfh *core.YFH) (*core.GithubFile, error) {
	gitfiles, err := yfh.Github.FindFantasyGame(ctx)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s in github", english.Plural(len(gitfiles), "fantasy game file", ""))
	if len(gitfiles) == 0 {
		content, err := Download(ctx, yfh, core.YahooFantasyGameURL())
		if err != nil {
			return nil, err
		}
		if err := yfh.Github.CreateFantasyGame(ctx, content); err != nil {
			return nil, err
		}
		gitfiles, err = yfh.Github.FindFantasyGame(ctx)
		if err != nil {
			return nil, err
		}
		if len(gitfiles) == 0 {
			return nil, errors.New("should have found at least one file for fantasy games")
		}
	}
	return gitfiles[0], nil
}

func HandleImportFantasyGame(ctx context.Context, yfh *core.YFH) error {
	// check if the fantasy game is in the github cache first
	gitfile, err := EnsureRecentFantasyGame(ctx, yfh)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.FantasyGameDirectory, gitfile.Filename()))
	if err != nil {
		return err
	}
	fg := fantasy.Game.ToFantasyGameModel()
	fg.GithubTimestamp = gitfile.Time
	log.Debugf("Ensuring fantasy game")
	return fg.Ensure(yfh.GormDB)
}
