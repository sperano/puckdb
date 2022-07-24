package worker

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func DownloadAndSaveTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.LocalFile, error) {
	var files []*core.LocalFile
	// download it if none are found then save it locally
	content, err := Download(ctx, yfh, core.TeamSummaryURL(teamID, date))
	if err != nil {
		return nil, err
	}
	if err := yfh.Local.CreateTeamSummary(ctx, teamID, date, content); err != nil {
		return nil, err
	}
	files, err = yfh.Local.FindTeamSummary(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("should have found at least one file for roster of team summary %02d on %04d-0%02d-%02d", teamID,
			date.Year(), date.Month(), date.Day())
	}
	return files[0], nil
}

func getDateStr(date time.Time) string {
	return fmt.Sprintf("%4d-%02d-%02d", date.Year(), date.Month(), date.Day())
}

/**
 * Ensure there is a file for a given roster for a given team on a given day
 */
func EnsureRecentTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.LocalFile, error) {
	log.Debug().
		Str("date", getDateStr(date)).
		Uint("team", teamID).
		Msg("EnsureTeamSummary")
	// find the files for the roster for that team on that date
	files, err := yfh.Local.FindTeamSummary(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	log.Debug().
		Str("date", getDateStr(date)).
		Uint("team", teamID).
		Msg(fmt.Sprintf("Found %s", english.Plural(len(files), "roster", "")))

	if len(files) == 0 {
		return DownloadAndSaveTeamSummary(ctx, yfh, teamID, date)
	}
	// return the latest
	return files[0], nil
}

func TeamSummaryGenerator() <-chan uint {
	out := make(chan uint)
	go func() {
		defer close(out)
		for _, id := range core.GetTeamIDs() {
			out <- id
		}
	}()
	return out
}

func importTeamSummaryStage(ctx context.Context, yfh *core.YFH, date time.Time, teamIDs <-chan uint) (<-chan *model.TeamSummary, <-chan error) {
	out := make(chan *model.TeamSummary)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		for teamID := range teamIDs {
			teamStats, err := doImportTeamSummary(ctx, yfh, teamID, date)
			if err != nil {
				errc <- err
				return
			}
			if teamStats != nil {
				out <- teamStats
			}
		}
	}()
	return out, errc
}

func importTeamSummaryPipeline(ctx context.Context, yfh *core.YFH, date time.Time) error {
	maxTeamSummaryImporter := viper.GetInt(core.FlagMaxTeamSummariesImporter)
	errcs := make([]<-chan error, maxTeamSummaryImporter)

	teamIDs := RostersGenerator()
	teamStatsStages := make([]<-chan *model.TeamSummary, maxTeamSummaryImporter)
	for i := 0; i < maxTeamSummaryImporter; i++ {
		stage, errc := importTeamSummaryStage(ctx, yfh, date, teamIDs)
		teamStatsStages[i] = stage
		errcs[i] = errc
	}
	go func() {
		for teamStats := range core.FanIn(ctx, teamStatsStages...) {
			log.Info().
				Str("date", getDateStr(date)).
				Uint("team", teamStats.TeamID).
				Msg("Team summary imported")
		}
		log.Info().
			Str("date", getDateStr(date)).
			Msg("All rosters imported")

	}()
	if err := core.WaitForPipeline(errcs...); err != nil {
		return err
	}
	return nil
}

func doImportTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*model.TeamSummary, error) {
	file, err := EnsureRecentTeamSummary(ctx, yfh, teamID, date)
	if err != nil {
		return nil, fmt.Errorf("EnsureRecentTeamSummary teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	fantasy, err := yfh.Local.ParseXML(ctx, path.Join(core.TeamSummaryDir(teamID), file.Filename()))
	if err != nil {
		log.Info().
			Str("date", getDateStr(date)).
			Uint("team", teamID).
			Err(err).
			Msg("Can't parse the team summary file")

		// let's try to download and parse it again
		file, err = DownloadAndSaveTeamSummary(ctx, yfh, teamID, date)
		if err != nil {
			return nil, err
		}
		fantasy, err = yfh.Local.ParseXML(ctx, path.Join(core.TeamSummaryDir(teamID), file.Filename()))
		if err != nil {
			return nil, err
		}
	}
	model, err := fantasy.Team.ToTeamSummaryModel()
	if err != nil {
		return nil, fmt.Errorf("ToTeamSummaryModel teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	log.Debug().
		Str("date", getDateStr(date)).
		Uint("team", model.TeamID).
		Msg("Ensuring team summary")
	if err := model.Ensure(yfh.GormDB); err != nil {
		return nil, fmt.Errorf("TeamSummaryModel teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	return model, nil
}

func HandleImportTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) error {
	_, err := doImportTeamSummary(ctx, yfh, teamID, date)
	return err
}

/**
 * This function handle importing all team summary for a specific day
 */
func HandleImportTeamSummaries(ctx context.Context, yfh *core.YFH, date time.Time) error {
	return importTeamSummaryPipeline(ctx, yfh, date)
}
