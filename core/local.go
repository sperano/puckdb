package core

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"time"

	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/rs/zerolog/log"
	flag "github.com/spf13/pflag"
)

const FlagDataPath = "data_path"

func SetupViperDataPath(flags *flag.FlagSet) {
	FString(flags, FlagDataPath, "", "Path to data")
}

type LocalClient struct {
	Path string
}

func NewLocalClient(path string) *LocalClient {
	return &LocalClient{
		Path: path,
	}
}

func (l *LocalClient) ReadDir(ctx context.Context, dir string) ([]*LocalFile, error) {
	entries, err := os.ReadDir(path.Join(l.Path, dir))
	if err != nil {
		return nil, err
	}
	files := []*LocalFile{}
	for _, f := range entries {
		cf := ParseLocalFilename(f.Name())
		if cf != nil {
			files = append(files, cf)
		}
	}
	return files, nil
}

func (l *LocalClient) Find(ctx context.Context, dir string, name string, extension string) ([]*LocalFile, error) {
	foundFiles := []*LocalFile{}
	cfs, err := l.ReadDir(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, cf := range cfs {
		if cf.Name == name && cf.Extension == extension {
			foundFiles = append(foundFiles, cf)
		}
	}
	sort.Slice(foundFiles, func(i, j int) bool {
		return foundFiles[i].Time.Unix() > foundFiles[j].Time.Unix()
	})
	return foundFiles, nil
}

func (l *LocalClient) Has(ctx context.Context, dir string, filename string, extension string) (bool, error) {
	cfs, err := l.Find(ctx, dir, filename, extension)
	if err != nil {
		return false, err
	}
	return len(cfs) > 0, nil
}

func (l *LocalClient) CreateFile(ctx context.Context, filepath string, content []byte) error {
	log.Info().Str("path", filepath).Msg("Importing")
	return ioutil.WriteFile(path.Join(l.Path, filepath), content, 0644)
}

func (l *LocalClient) ReadFile(ctx context.Context, filepath string) ([]byte, error) {
	log.Debug().Str("path", filepath).Msg("Reading")
	return ioutil.ReadFile(path.Join(l.Path, filepath))
}

//////////////////////////////////////////////////////////////////////////////
// FANTASY GAME
//////////////////////////////////////////////////////////////////////////////
const FantasyGameDirectory = "fantasy-game"
const FantasyGameFilename = FantasyGameDirectory
const FantasyGameExtension = "xml"

func YahooFantasyGameURL() string {
	return fmt.Sprintf("%s/game/nhl", BaseAPIURL)
}

func (l *LocalClient) FindFantasyGame(ctx context.Context) ([]*LocalFile, error) {
	return l.Find(ctx, FantasyGameDirectory, FantasyGameFilename, FantasyGameExtension)
}

func (l *LocalClient) CreateFantasyGame(ctx context.Context, content []byte) error {
	fn := GetLocalFilename(FantasyGameFilename, FantasyGameExtension, time.Now())
	return l.CreateFile(ctx, path.Join(FantasyGameDirectory, fn), content)
}

//////////////////////////////////////////////////////////////////////////////
// LEAGUE
//////////////////////////////////////////////////////////////////////////////
const LeagueDirectory = "league"
const LeagueFilename = LeagueDirectory
const LeagueExtension = "xml"

func YahooLeagueURL(leagueID int) string {
	return fmt.Sprintf("%s/league/%d.l.%d", BaseAPIURL, GameConst, leagueID)
}

func (l *LocalClient) FindLeague(ctx context.Context) ([]*LocalFile, error) {
	return l.Find(ctx, LeagueDirectory, LeagueFilename, LeagueExtension)
}

func (l *LocalClient) CreateLeague(ctx context.Context, content []byte) error {
	fn := GetLocalFilename(LeagueFilename, LeagueExtension, time.Now())
	return l.CreateFile(ctx, path.Join(LeagueDirectory, fn), content)
}

//////////////////////////////////////////////////////////////////////////////
// TEAMS
//////////////////////////////////////////////////////////////////////////////

func YahooTeamURL(leagueID int, teamID uint) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d", BaseAPIURL, GameConst, leagueID, teamID)
}

func GetTeamDir(teamID uint) string {
	return fmt.Sprintf("teams/team-%02d", teamID)
}

func GetTeamFilename(teamID uint) string {
	return fmt.Sprintf("team-%02d", teamID)
}

const TeamExtension = "xml"

func (l *LocalClient) FindTeam(ctx context.Context, teamID uint) ([]*LocalFile, error) {
	dir := GetTeamDir(teamID)
	fn := GetTeamFilename(teamID)
	return l.Find(ctx, dir, fn, TeamExtension)
}

func (l *LocalClient) CreateTeam(ctx context.Context, teamID uint, content []byte) error {
	fn := GetLocalFilename(GetTeamFilename(teamID), TeamExtension, time.Now())
	p := path.Join(GetTeamDir(teamID), fn)
	return l.CreateFile(ctx, p, content)
}

//////////////////////////////////////////////////////////////////////////////
// Games
//////////////////////////////////////////////////////////////////////////////
const GamesDir = "games"
const GamesListExtension = "html"
const GameExtension = "html"

func GamesListPath(date time.Time) string {
	return path.Join(GameDir(date), GamesListFilename(date))
}

func GamesListFilename(date time.Time) string {
	return fmt.Sprintf("games-list-%4d-%02d-%02d", date.Year(), date.Month(), date.Day())
}

func GamesListURL(date time.Time) string {
	return fmt.Sprintf("%s/nhl/scoreboard/?confId=&dateRange=%4d-%02d-%02d&schedState=2", BaseSportsURL, date.Year(), date.Month(), date.Day())
}

func (l *LocalClient) FindGamesList(ctx context.Context, date time.Time) ([]*LocalFile, error) {
	fn := GamesListFilename(date)
	return l.Find(ctx, GameDir(date), fn, GamesListExtension)
}

func (l *LocalClient) CreateGamesList(ctx context.Context, date time.Time, content []byte) error {
	fn := GetLocalFilename(GamesListFilename(date), GamesListExtension, time.Now())
	p := path.Join(GameDir(date), fn)
	return l.CreateFile(ctx, p, content)
}

func GameURL(gameLink string) string {
	return fmt.Sprintf("%s/%s", BaseSportsURL, gameLink)
}

func GameDir(date time.Time) string {
	return fmt.Sprintf("%s/%4d/%02d/%02d", GamesDir, date.Year(), date.Month(), date.Day())
}

func GamePath(date time.Time, gamelink string) string {
	return path.Join(GameDir(date), gamelink)
}

func (l *LocalClient) FindGame(ctx context.Context, date time.Time, gameLink string) ([]*LocalFile, error) {
	log.Debug().Str("game", gameLink).Msg("LocalClient.FindGame")
	return l.Find(ctx, GameDir(date), gameLink, GameExtension)
}

func (l *LocalClient) CreateGame(ctx context.Context, date time.Time, gameLink string, content []byte) error {
	fn := GetLocalFilename(gameLink, GameExtension, time.Now())
	p := path.Join(GameDir(date), fn)
	return l.CreateFile(ctx, p, content)
}

//////////////////////////////////////////////////////////////////////////////
// ROSTER
//////////////////////////////////////////////////////////////////////////////
const RostersExtension = "xml"

func RostersDir(teamID uint) string {
	return fmt.Sprintf("rosters/%02d", teamID)
}

func RosterFilename(teamID uint, date time.Time) string {
	return fmt.Sprintf("rosters-%02d-%4d-%02d-%02d", teamID, date.Year(), date.Month(), date.Day())
}

func RosterPath(teamID uint, date time.Time) string {
	return path.Join(RostersDir(teamID), RosterFilename(teamID, date))
}

func RosterURL(teamID uint, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1/stats;type=season
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players", BaseAPIURL, GameConst, GetLeagueID(), teamID, date.Year(), date.Month(), date.Day())
}

func (l *LocalClient) FindRoster(ctx context.Context, teamID uint, date time.Time) ([]*LocalFile, error) {
	return l.Find(ctx, RostersDir(teamID), RosterFilename(teamID, date), RostersExtension)
}

func (l *LocalClient) CreateRoster(ctx context.Context, teamID uint, date time.Time, content []byte) error {
	fn := GetLocalFilename(RosterFilename(teamID, date), RostersExtension, time.Now())
	p := path.Join(RostersDir(teamID), fn)
	return l.CreateFile(ctx, p, content)
}

//////////////////////////////////////////////////////////////////////////////
// TEAM SUMMARY
//////////////////////////////////////////////////////////////////////////////
const TeamSummaryExtension = "xml"

func TeamSummaryDir(teamID uint) string {
	return fmt.Sprintf("summaries/team-%02d", teamID)
}

func TeamSummaryFilename(teamID uint, date time.Time) string {
	return fmt.Sprintf("team-%02d-summary-%4d-%02d-%02d", teamID, date.Year(), date.Month(), date.Day())
}

func TeamSummaryPath(teamID uint, date time.Time) string {
	return path.Join(TeamSummaryDir(teamID), TeamSummaryFilename(teamID, date))
}

func TeamSummaryURL(teamID uint, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/253.l.1004.t.10/stats;type=date;date=2011-07-06
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/stats;type=date;date=%d-%02d-%02d", BaseAPIURL, GameConst, GetLeagueID(), teamID, date.Year(), date.Month(), date.Day())
}

func (l *LocalClient) FindTeamSummary(ctx context.Context, teamID uint, date time.Time) ([]*LocalFile, error) {
	return l.Find(ctx, TeamSummaryDir(teamID), TeamSummaryFilename(teamID, date), TeamSummaryExtension)
}

func (l *LocalClient) CreateTeamSummary(ctx context.Context, teamID uint, date time.Time, content []byte) error {
	fn := GetLocalFilename(TeamSummaryFilename(teamID, date), TeamSummaryExtension, time.Now())
	p := path.Join(TeamSummaryDir(teamID), fn)
	return l.CreateFile(ctx, p, content)
}

/////////////////////////////

func (l *LocalClient) ParseXML(ctx context.Context, path string) (*xmlmodel.FantasyContent, error) {
	data, err := l.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	var fantasy xmlmodel.FantasyContent
	if err := xml.Unmarshal(data, &fantasy); err != nil {
		return nil, err
	}
	return &fantasy, nil
}

func (l *LocalClient) GetTmpCopy(ctx context.Context, prefix string, path string) (string, error) {
	tmpfile, err := ioutil.TempFile("", prefix)
	if err != nil {
		log.Fatal().Err(err)
	}
	data, err := l.ReadFile(ctx, path)
	if err != nil {
		return "", err
	}
	_, err = tmpfile.Write(data)
	if err != nil {
		return "", err
	}
	err = tmpfile.Close()
	if err != nil {
		return "", err
	}
	return tmpfile.Name(), nil
}
