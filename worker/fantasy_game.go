package worker

import (
	"context"
	"errors"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/rs/zerolog/log"
)

func EnsureRecentFantasyGame(ctx context.Context, yfh *core.YFH) (*core.LocalFile, error) {
	files, err := yfh.Local.FindFantasyGame(ctx)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Found %s", english.Plural(len(files), "fantasy game file", ""))
	if len(files) == 0 {
		content, err := Download(ctx, yfh, core.YahooFantasyGameURL())
		if err != nil {
			return nil, err
		}
		if err := yfh.Local.CreateFantasyGame(ctx, content); err != nil {
			return nil, err
		}
		files, err = yfh.Local.FindFantasyGame(ctx)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errors.New("should have found at least one file for fantasy games")
		}
	}
	return files[0], nil
}

func HandleImportFantasyGame(ctx context.Context, yfh *core.YFH) error {
	file, err := EnsureRecentFantasyGame(ctx, yfh)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Local.ParseXML(ctx, path.Join(core.FantasyGameDirectory, file.Filename()))
	if err != nil {
		return err
	}
	fg := fantasy.Game.ToFantasyGameModel()
	fg.GithubTimestamp = file.Time
	log.Debug().Msg("Ensuring fantasy game")
	return fg.Ensure(yfh.GormDB)
}
