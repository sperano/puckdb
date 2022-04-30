package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/k0kubun/pp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const FlagMaxGamesImporter = "max_games_importer"

/**
 * Ensure there is a file in github for a given game on a given day
 */
func EnsureRecentGame(ctx context.Context, yfh *core.YFH, date time.Time, gameLink string) (*core.GithubFile, error) {
	log.Debugf("EnsureRecentGame date: %4d-%02d-%02d", date.Year(), date.Month(), date.Day())
	tokens := strings.Split(gameLink, "/")
	gameName := tokens[len(tokens)-2]
	log.Debugf("Game name: %s", gameName)
	// fetch the files in github for the game on that date
	gitfiles, err := yfh.Github.FindGame(ctx, date, gameName)
	if err != nil {
		return nil, err
	}
	log.Infof("Found %s for %s in github", english.Plural(len(gitfiles), "game", ""), gameName)
	if len(gitfiles) == 0 {
		// download it if none are found then add it in github
		content, err := Download(ctx, yfh, core.GameURL(gameLink))
		if err != nil {
			return nil, err
		}
		if err := yfh.Github.CreateGame(ctx, date, gameName, content); err != nil {
			return nil, err
		}
		// get the gitfile for what we just created, it's from the cache so it's quick
		gitfiles, err = yfh.Github.FindGame(ctx, date, gameName)
		if err != nil {
			return nil, err
		}
		if len(gitfiles) == 0 {
			return nil, fmt.Errorf("should have found at least one file for game %s", gameName)
		}
	}
	// return the latest
	return gitfiles[0], nil
}

func importGameStage(ctx context.Context, yfh *core.YFH, date time.Time, gamelinks <-chan string) (<-chan *model.Game, <-chan error) {
	out := make(chan *model.Game)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		for gamelink := range gamelinks {
			game, err := doImportGame(ctx, yfh, date, gamelink)
			if err != nil {
				errc <- err
				return
			}
			// could be nil if it's not a regular season game (all star game)
			if game != nil {
				out <- game
			}
		}
	}()
	return out, errc
}

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

func importGamesPipeline(ctx context.Context, yfh *core.YFH, filename string, date time.Time) error {
	maxGamesImporter := viper.GetInt(FlagMaxGamesImporter)
	errcs := make([]<-chan error, maxGamesImporter)

	gamelinks := GameListGenerator(filename)
	gamesStages := make([]<-chan *model.Game, maxGamesImporter)
	for i := 0; i < maxGamesImporter; i++ {
		stage, errc := importGameStage(ctx, yfh, date, gamelinks)
		gamesStages[i] = stage
		errcs[i] = errc
	}
	go func() {
		count := 0
		for game := range core.FanIn(ctx, gamesStages...) {
			count++
			log.Infof("Game import-checked: %s\n", game.Name)
		}
		if count == 0 {
			log.Warnf("%04d-%02d-%02d has no games", date.Year(), date.Month(), date.Day())
		} else {
			log.Infof("%s on %04d-%02d-%02d", english.Plural(count, "game", ""), date.Year(), date.Month(), date.Day())
		}
	}()
	if err := core.WaitForPipeline(errcs...); err != nil {
		return err
	}
	return nil
}

/**
 * This function handle importing all games for a specific day
 */
func HandleImportGames(ctx context.Context, yfh *core.YFH, date time.Time) error {
	// check if the game list is in the github cache first
	gitfile, err := EnsureRecentGamesList(ctx, yfh, date)
	if err != nil {
		return err
	}
	// get a local tmp copy of the games list for parsing
	localFilename, err := yfh.Github.GetTmpCopy(ctx, "games-list", path.Join(core.GameDir(date), gitfile.Filename()))
	if err != nil {
		return err
	}
	defer func() {
		log.Debugf("Removing tmp file: %s", localFilename)
		os.Remove(localFilename)
	}()
	// let's parse the games list to find individual games
	return importGamesPipeline(ctx, yfh, localFilename, date)
}

func doImportGame(ctx context.Context, yfh *core.YFH, date time.Time, gamelink string) (*model.Game, error) {
	log.Debugf("URL: %s", gamelink)
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
	log.Infof("Ensuring game %04d-%02d-%02d %d (%d) - %d (%d)", date.Year(), date.Month(), date.Day(),
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
}

func HandleImportGame(ctx context.Context, yfh *core.YFH, date time.Time, gamelink string) error {
	_, err := doImportGame(ctx, yfh, date, gamelink)
	return err
}
