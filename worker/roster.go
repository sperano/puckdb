package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

/**
 * Ensure there is a file in github for a given roster for a given team on a given day
 */
func EnsureRecentRoster(ctx context.Context, yfh *core.YFH, teamID int, date time.Time) (*core.GithubFile, error) {
	log.Debugf("EnsureRecentRoster date: %4d-%02d-%02d team: %d", date.Year(), date.Month(), date.Day(), teamID)
	// fetch the files in github for the roster for that team on that date
	gitfiles, err := yfh.Github.FindRoster(ctx, teamID, date)
	if err != nil {
		return nil, err
	}
	log.Debugf("Found %s for team %02d on %04d-%02d-%02d in github", english.Plural(len(gitfiles), "roster", ""), teamID,
		date.Year(), date.Month(), date.Day())
	if len(gitfiles) == 0 {
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
	}
	// return the latest
	return gitfiles[0], nil
}

/*

func findJSON(content []byte, gameLink string) (string, error) {
	const prefix = "root.App.main = "
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, prefix) {
			// found it, parsing it
			return line[len(prefix) : len(line)-1], nil
		}
	}
	return "", fmt.Errorf("couldn't find json data in %s", gameLink)
}

func GetGameXMLModel(ctx context.Context, yfh *core.YFH, date time.Time, gameLink string) (*xmlmodel.Game, *core.GithubFile, error) {
	gitfile, err := EnsureRecentGame(ctx, yfh, date, gameLink)
	if err != nil {
		return nil, nil, err
	}
	content, err := yfh.Github.ReadFile(ctx, path.Join(core.GameDir(date), gitfile.Filename()))
	if err != nil {
		return nil, nil, err
	}
	// find the json data in the file
	jsonData, err := findJSON(content, gameLink)
	if err != nil {
		return nil, nil, err
	}
	var game xmlmodel.Game
	if err := json.Unmarshal([]byte(jsonData), &game); err != nil {
		// one "valid" error is that the game was postponed
		if game.IsPostponed() {
			log.Warnf("Game %s was postponed", gameLink)
		} else {
			return nil, nil, err
		}
	}
	return &game, gitfile, nil
}

func EnsureNHL(ctx context.Context, db *gorm.DB, game *xmlmodel.Game) error {
	if err := EnsureNHLTeam(ctx, db, game.HomeTeam()); err != nil {
		return fmt.Errorf("error with home team -> %w", err)
	}
	if err := EnsureNHLTeam(ctx, db, game.AwayTeam()); err != nil {
		return fmt.Errorf("error with away team -> %w", err)
	}
	return EnsureNHLPlayers(ctx, db, game.Players())
}
*/

func RostersGenerator() <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for _, id := range core.GetTeamIds() {
			out <- id
		}
	}()
	return out
}

func importRosterStage(ctx context.Context, yfh *core.YFH, date time.Time, teamIDs <-chan int) (<-chan []*model.RosterPlayer, <-chan error) {
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

func doImportRoster(ctx context.Context, yfh *core.YFH, teamID int, date time.Time) ([]*model.RosterPlayer, error) {
	// check if the game list is in the github cache first
	gitfile, err := EnsureRecentRoster(ctx, yfh, teamID, date)
	if err != nil {
		return nil, err
	}
	_ = gitfile
	/*
		game, gitfile, err := GetGameXMLModel(ctx, yfh, date, gamelink)
		if err != nil {
			return nil, fmt.Errorf("GetGamesXMLModel (%s) -> %w", gamelink, err)
		}
		if !game.IsRegularSeason() {
			log.Warnf("Not a regular season game: %s", gamelink)
			return nil, nil
		}
		log.Debug(pp.Sprint(game))

		if err := EnsureNHL(ctx, yfh.GormDB, game); err != nil {
			return nil, fmt.Errorf("EnsureNHL (%s) -> %w", gamelink, err)
		}
		homeTeamID, err := game.HomeTeam().TeamID.Id()
		if err != nil {
			return nil, err
		}
		homeTeamScore, err := game.HomeTeamScore()
		if err != nil {
			return nil, err
		}
		awayTeamID, err := game.AwayTeam().TeamID.Id()
		if err != nil {
			return nil, err
		}
		awayTeamScore, err := game.AwayTeamScore()
		if err != nil {
			return nil, err
		}
		newGame := model.Game{
			Date:            date,
			HomeTeamID:      homeTeamID,
			HomeTeamScore:   uint(homeTeamScore),
			AwayTeamID:      awayTeamID,
			AwayTeamScore:   uint(awayTeamScore),
			State:           game.GetState(),
			Name:            gamelink[5 : len(gamelink)-1],
			GithubTimestamp: gitfile.Time, // TODO not good timezone?
		}
		log.Debugf("Ensuring game %04d-%02d-%02d %d (%d) - %d (%d)", date.Year(), date.Month(), date.Day(),
			homeTeamID, homeTeamScore, awayTeamID, awayTeamScore)
		if err := yfh.GormDB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "date"}, {Name: "home_team_id"}, {Name: "away_team_id"}}, // TODO constants
			// TODO move that list elsewhere
			DoUpdates: clause.AssignmentColumns([]string{"home_team_score", "away_team_score", "state", "name", "github_timestamp"}),
		}).Create(&newGame).Error; err != nil {
			return nil, err
		}
		stats, err := game.PlayerStats()
		if err != nil {
			return nil, err
		}
		for _, ps := range stats {
			playerStats, err := ps.ToPlayerStatsModel()
			if err != nil {
				return nil, err
			}
			playerStats.Date = date
			logPlayerStats(date, playerStats)

			if err := yfh.GormDB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "date"}, {Name: "player_id"}}, // TODO constants
				DoUpdates: clause.AssignmentColumns(model.PlayerStatsUpdateCols),
			}).Create(&playerStats).Error; err != nil {
				return nil, err
			}
		}
		return &newGame, nil
	*/
	return nil, nil
}

func HandleImportRoster(ctx context.Context, yfh *core.YFH, teamID int, date time.Time) error {
	_, err := doImportRoster(ctx, yfh, teamID, date)
	return err
}

/**
 * This function handle importing all rosters for a specific day
 */
func HandleImportRosters(ctx context.Context, yfh *core.YFH, date time.Time) error {
	return importRostersPipeline(ctx, yfh, date)
}
