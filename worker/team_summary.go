package worker

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func DownloadAndSaveTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.GithubFile, error) {
	var gitfiles []*core.GithubFile
	// download it if none are found then add it in github
	content, err := Download(ctx, yfh, core.TeamSummaryURL(teamID, date))
	if err != nil {
		return nil, err
	}
	if err := yfh.Github.CreateTeamSummary(ctx, teamID, date, content); err != nil {
		return nil, err
	}
	// get the gitfile for what we just cre ated, it's from the cache so it's quick
	gitfiles, err = yfh.Github.FindTeamSummary(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	if len(gitfiles) == 0 {
		return nil, fmt.Errorf("should have found at least one file for roster of team summary %02d on %04d-0%02d-%02d in github", teamID,
			date.Year(), date.Month(), date.Day())
	}
	return gitfiles[0], nil
}

/**
 * Ensure there is a file in github for a given roster for a given team on a given day
 */
func EnsureRecentTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.GithubFile, error) {
	log.Debugf("EnsureTeamSummary date: %4d-%02d-%02d team: %d", date.Year(), date.Month(), date.Day(), teamID)
	// fetch the files in github for the roster for that team on that date
	gitfiles, err := yfh.Github.FindTeamSummary(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s for team #%02d stats on %04d-%02d-%02d in github", english.Plural(len(gitfiles), "roster", ""), teamID,
		date.Year(), date.Month(), date.Day())
	if len(gitfiles) == 0 {
		return DownloadAndSaveTeamSummary(ctx, yfh, teamID, date)
	}
	// return the latest
	return gitfiles[0], nil
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
			log.Infof("Team summary imported: team %02d %04d-%02d-%02d", teamStats.TeamID, date.Year(), date.Month(), date.Day())
		}
		log.Infof("Imported all rosters for %04d-%02d-%02d", date.Year(), date.Month(), date.Day())
	}()
	if err := core.WaitForPipeline(errcs...); err != nil {
		return err
	}
	return nil
}

func doImportTeamSummary(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*model.TeamSummary, error) {
	// check if the roster is in the github cache first
	gitfile, err := EnsureRecentTeamSummary(ctx, yfh, teamID, date)
	if err != nil {
		return nil, fmt.Errorf("EnsureRecentTeamSummary teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.TeamSummaryDir(teamID), gitfile.Filename()))
	if err != nil {
		log.Warnf("Can't parse the team summary file for teamID=%02d date=%4d-%02d-%02d error=%s",
			teamID, date.Year(), date.Month(), date.Day(), err)
		// let's try to download and parse it again
		gitfile, err = DownloadAndSaveTeamSummary(ctx, yfh, teamID, date)
		if err != nil {
			return nil, err
		}
		fantasy, err = yfh.Github.ParseXML(ctx, path.Join(core.TeamSummaryDir(teamID), gitfile.Filename()))
		if err != nil {
			return nil, err
		}
	}
	model, err := fantasy.Team.ToTeamSummaryModel()
	if err != nil {
		return nil, fmt.Errorf("ToTeamSummaryModel teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	log.Debugf("Ensuring team summary %04d-%02d-%02d team:%02d", date.Year(), date.Month(), date.Day(), model.TeamID)
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
