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

func DownloadAndSaveRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.GithubFile, error) {
	var gitfiles []*core.GithubFile
	// download it if none are found then add it in github
	content, err := Download(ctx, yfh, core.RosterURL(teamID, date))
	if err != nil {
		return nil, err
	}
	if err := yfh.Github.CreateRoster(ctx, teamID, date, content); err != nil {
		return nil, err
	}
	// get the gitfile for what we just cre ated, it's from the cache so it's quick
	gitfiles, err = yfh.Github.FindRoster(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	if len(gitfiles) == 0 {
		return nil, fmt.Errorf("should have found at least one file for roster of team %02d on %04d-0%02d-%02d in github", teamID,
			date.Year(), date.Month(), date.Day())
	}
	return gitfiles[0], nil
}

/**
 * Ensure there is a file in github for a given roster for a given team on a given day
 */
func EnsureRecentRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.GithubFile, error) {
	log.Debugf("EnsureRecentRoster date: %4d-%02d-%02d team: %d", date.Year(), date.Month(), date.Day(), teamID)
	// fetch the files in github for the roster for that team on that date
	gitfiles, err := yfh.Github.FindRoster(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s for team %02d on %04d-%02d-%02d in github", english.Plural(len(gitfiles), "roster", ""), teamID,
		date.Year(), date.Month(), date.Day())
	if len(gitfiles) == 0 {
		/*
			// download it if none are found then add it in github
			content, err := Download(ctx, yfh, core.RosterURL(teamID, date))
			if err != nil {
				return nil, err
			}
			if err := yfh.Github.CreateRoster(ctx, teamID, date, content); err != nil {
				return nil, err
			}
			// get the gitfile for what we just cre ated, it's from the cache so it's quick
			gitfiles, err = yfh.Github.FindRoster(ctx, teamID, date)
			if err != nil {
				return nil, err
			}
			if len(gitfiles) == 0 {
				return nil, fmt.Errorf("should have found at least one file for roster of team %02d on %04d-0%02d-%02d in github", teamID,
					date.Year(), date.Month(), date.Day())
			}
		*/
		return DownloadAndSaveRoster(ctx, yfh, teamID, date)
	}
	// return the latest
	return gitfiles[0], nil
}

func RostersGenerator() <-chan uint {
	out := make(chan uint)
	go func() {
		defer close(out)
		for _, id := range core.GetTeamIDs() {
			out <- id
		}
	}()
	return out
}

func importRosterStage(ctx context.Context, yfh *core.YFH, date time.Time, teamIDs <-chan uint) (<-chan []*model.RosterPlayer, <-chan error) {
	out := make(chan []*model.RosterPlayer)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		for teamID := range teamIDs {
			rosterPlayers, err := doImportRoster(ctx, yfh, teamID, date)
			if err != nil {
				errc <- err
				return
			}
			if rosterPlayers != nil {
				out <- rosterPlayers
			}
		}
	}()
	return out, errc
}

func importRostersPipeline(ctx context.Context, yfh *core.YFH, date time.Time) error {
	maxRostersImporter := viper.GetInt(core.FlagMaxRostersImporter)
	errcs := make([]<-chan error, maxRostersImporter)

	teamIDs := RostersGenerator()
	rostersStages := make([]<-chan []*model.RosterPlayer, maxRostersImporter)
	for i := 0; i < maxRostersImporter; i++ {
		stage, errc := importRosterStage(ctx, yfh, date, teamIDs)
		rostersStages[i] = stage
		errcs[i] = errc
	}
	go func() {
		for rosterPlayers := range core.FanIn(ctx, rostersStages...) {
			log.Infof("Rosters imported: team %02d %04d-%02d-%02d", rosterPlayers[0].TeamID, date.Year(), date.Month(), date.Day())
		}
		log.Infof("Imported all rosters for %04d-%02d-%02d", date.Year(), date.Month(), date.Day())
	}()
	if err := core.WaitForPipeline(errcs...); err != nil {
		return err
	}
	return nil
}

func doImportRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) ([]*model.RosterPlayer, error) {
	// check if the roster is in the github cache first
	gitfile, err := EnsureRecentRoster(ctx, yfh, teamID, date)
	if err != nil {
		return nil, fmt.Errorf("EnsureRecentRoster teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.RostersDir(teamID), gitfile.Filename()))
	if err != nil {
		log.Warnf("Can't parse the roster file for teamID=%02d date=%4d-%02d-%02d error=%s",
			teamID, date.Year(), date.Month(), date.Day(), err)
		// let's try to download and parse it again
		gitfile, err = DownloadAndSaveRoster(ctx, yfh, teamID, date)
		if err != nil {
			return nil, err
		}
		fantasy, err = yfh.Github.ParseXML(ctx, path.Join(core.RostersDir(teamID), gitfile.Filename()))
		if err != nil {
			return nil, err
		}
	}
	models, err := fantasy.Team.ToRosterPlayersModel()
	if err != nil {
		return nil, fmt.Errorf("ToRosterPlayersModel teamID=%02d date=%4d-%02d-%02d error=%w",
			teamID, date.Year(), date.Month(), date.Day(), err)
	}
	for _, rp := range models {
		log.Debugf("Ensuring player roster %04d-%02d-%02d team:%02d player:%d %s",
			date.Year(), date.Month(), date.Day(), rp.TeamID, rp.PlayerID, rp.SelectedPosition)
		if err := rp.Ensure(yfh.GormDB); err != nil {
			return nil, fmt.Errorf("RosterPlayerModel playerID=%d teamID=%02d date=%4d-%02d-%02d error=%w",
				rp.PlayerID, teamID, date.Year(), date.Month(), date.Day(), err)
		}
	}
	return models, nil
}

func HandleImportRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) error {
	_, err := doImportRoster(ctx, yfh, teamID, date)
	return err
}

/**
 * This function handle importing all rosters for a specific day
 */
func HandleImportRosters(ctx context.Context, yfh *core.YFH, date time.Time) error {
	return importRostersPipeline(ctx, yfh, date)
}
