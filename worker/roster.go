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

func DownloadAndSaveRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.LocalFile, error) {
	var files []*core.LocalFile
	// download it if none are found then save it locally
	content, err := Download(ctx, yfh, core.RosterURL(teamID, date))
	if err != nil {
		return nil, err
	}
	if err := yfh.Local.CreateRoster(ctx, teamID, date, content); err != nil {
		return nil, err
	}
	// get the file for what we just created
	files, err = yfh.Local.FindRoster(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("should have found at least one file for roster of team roster %02d on %04d-0%02d-%02d", teamID,
			date.Year(), date.Month(), date.Day())
	}
	return files[0], nil
}

/**
 * Ensure there is a file for a given roster for a given team on a given day
 */
func EnsureRecentRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) (*core.LocalFile, error) {
	ds := getDateStr(date)
	log.Debug().Str("date", ds).Uint("teamID", teamID).Msg("EnsureRecentRoster")
	// find the files for the roster for that team on that date
	files, err := yfh.Local.FindRoster(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	log.Debug().Str("date", ds).Uint("teamID", teamID).Msgf("Found %s", english.Plural(len(files), "roster", ""))
	if len(files) == 0 {
		return DownloadAndSaveRoster(ctx, yfh, teamID, date)
	}
	// return the latest
	return files[0], nil
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
		ds := getDateStr(date)
		for rosterPlayers := range core.FanIn(ctx, rostersStages...) {
			log.Info().Str("date", ds).Uint("teamID", rosterPlayers[0].TeamID).Msg("Rosters imported")
		}
		log.Info().Str("date", ds).Msg("Imported all rosters")
	}()
	if err := core.WaitForPipeline(errcs...); err != nil {
		return err
	}
	return nil
}

func doImportRoster(ctx context.Context, yfh *core.YFH, teamID uint, date time.Time) ([]*model.RosterPlayer, error) {
	ds := getDateStr(date)
	file, err := EnsureRecentRoster(ctx, yfh, teamID, date)
	if err != nil {
		return nil, fmt.Errorf("EnsureRecentRoster teamID=%02d date=%s error=%w", teamID, ds, err)
	}
	fantasy, err := yfh.Local.ParseXML(ctx, path.Join(core.RostersDir(teamID), file.Filename()))
	if err != nil {
		log.Warn().Str("date", ds).Uint("teamID", teamID).Err(err).Msg("Can't parse the roster file")
		// let's try to download and parse it again
		file, err = DownloadAndSaveRoster(ctx, yfh, teamID, date)
		if err != nil {
			return nil, err
		}
		fantasy, err = yfh.Local.ParseXML(ctx, path.Join(core.RostersDir(teamID), file.Filename()))
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
		log.Debug().Str("date", ds).Uint("teamID", rp.TeamID).Uint("playerID", rp.PlayerID).Int("pos", int(rp.SelectedPosition)).
			Msg("Ensuring player roster")
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
