package worker

import (
	"context"
	"errors"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func EnsureRecentLeague(ctx context.Context, yfh *core.YFH) (*core.LocalFile, error) {
	files, err := yfh.Local.FindLeague(ctx)
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("Found %s", english.Plural(len(files), "fantasy game file", ""))
	if len(files) == 0 {
		content, err := Download(ctx, yfh, core.YahooLeagueURL(viper.GetInt(core.FlagLeagueID)))
		if err != nil {
			return nil, err
		}
		if err := yfh.Local.CreateLeague(ctx, content); err != nil {
			return nil, err
		}
		files, err = yfh.Local.FindLeague(ctx)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errors.New("should have found at least one file for fantasy games")
		}
	}
	return files[0], nil
}

func HandleImportLeague(ctx context.Context, yfh *core.YFH) error {
	file, err := EnsureRecentLeague(ctx, yfh)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Local.ParseXML(ctx, path.Join(core.LeagueDirectory, file.Filename()))
	if err != nil {
		return err
	}
	league, err := fantasy.League.ToLeagueModel()
	if err != nil {
		return err
	}
	league.GithubTimestamp = file.Time
	log.Debug().Msg("Ensuring league")
	return league.Ensure(yfh.GormDB)
}
