package core

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"path"
	"sort"
	"time"

	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/google/go-github/v41/github"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

const FlagGithubAccessToken = "github_access_token"
const DataPath = "data"
const DataRepoOwner = "ericsperano"
const DataRepoName = "yahoo-fantasy-hockey-data"

func SetupViperGithubAccessToken(flags *flag.FlagSet) {
	FString(flags, FlagGithubAccessToken, "", "Github access token")
}

type RepositoriesService interface {
	CreateFile(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentFileOptions) (*github.RepositoryContentResponse, *github.Response, error)
	DownloadContents(ctx context.Context, owner, repo, filepath string, opts *github.RepositoryContentGetOptions) (io.ReadCloser, *github.Response, error)
	GetContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (fileContent *github.RepositoryContent, directoryContent []*github.RepositoryContent, resp *github.Response, err error)
}

type GithubClient struct {
	//client *github.Client
	Repositories RepositoriesService
	Cache        GithubCache
}

func Init3rdPartyGithubClient() *github.Client {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: viper.GetString(FlagGithubAccessToken)},
	)
	return github.NewClient(oauth2.NewClient(ctx, ts))
}

func NewGithubClient(cache GithubCache, repositories RepositoriesService) *GithubClient {
	return &GithubClient{
		Cache:        cache,
		Repositories: repositories,
	}
}

func (g *GithubClient) ReadDir(ctx context.Context, dir string) ([]*GithubFile, error) {
	files, err := g.Cache.GetDirectoryContents(ctx, dir)
	if err != nil {
		return nil, err
	}
	if files != nil {
		log.Debugf("Found directory contents for %s in cache: %d items", dir, len(files))
		return files, nil
	}
	log.Infof("No directory contents found for %s in cache", dir)
	d := path.Join(DataPath, dir)
	opts := github.RepositoryContentGetOptions{}
	_, dirContent, resp, err := g.Repositories.GetContents(ctx, "ericsperano", "yahoo-fantasy-hockey-data", d, &opts)
	if err != nil {
		gerr, ok := err.(*github.ErrorResponse)
		if !ok {
			return nil, err
		}
		if gerr.Response.StatusCode == http.StatusNotFound {
			log.Info("No directory contents found in github")
			return []*GithubFile{}, nil
		}
		return nil, err
	}
	files = []*GithubFile{}
	if resp.StatusCode == http.StatusNotFound {
		return files, nil
	}
	for _, f := range dirContent {
		cf := ParseGithubFilename(f.GetName())
		if cf != nil {
			files = append(files, cf)
		}
	}
	if err := g.Cache.SetDirectoryContents(ctx, dir, files); err != nil {
		return nil, err
	}
	return files, nil
}

func (g *GithubClient) Find(ctx context.Context, dir string, name string, extension string) ([]*GithubFile, error) {
	foundFiles := []*GithubFile{}
	cfs, err := g.ReadDir(ctx, dir)
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

func (g *GithubClient) Has(ctx context.Context, dir string, filename string, extension string) (bool, error) {
	cfs, err := g.Find(ctx, dir, filename, extension)
	if err != nil {
		return false, err
	}
	return len(cfs) > 0, nil
}

func (g *GithubClient) CreateFile(ctx context.Context, filepath string, message string, content []byte) error {
	p := path.Join(DataPath, filepath)

	log.Infof("Importing %s", p)
	opts := github.RepositoryContentFileOptions{
		Message: &message,
		Content: content,
	}
	retries := 10
	for {
		if _, _, err := g.Repositories.CreateFile(ctx, DataRepoOwner, DataRepoName, p, &opts); err == nil {
			break
		} else {
			log.Warnf("g.Repositories.CreateFile: %s (will start retry #%d)", err, 11-retries)
			retries--
			if retries == 0 {
				log.Error(err)
				return fmt.Errorf("g.Repositories.CreateFile: %w", err)
			}
			time.Sleep(time.Duration(rand.Intn(125)+25) * time.Millisecond)
		}
	}
	if err := g.Cache.SetFile(ctx, filepath, content); err != nil {
		return err
	}
	return g.Cache.InvalidateDirectoryContents(ctx, path.Dir(filepath))
}

func (g *GithubClient) ReadFile(ctx context.Context, filepath string) ([]byte, error) {
	data, err := g.Cache.GetFile(ctx, filepath)
	if err != nil {
		return nil, err
	}
	if data != nil {
		log.Debugf("Found file content for %s in cache", filepath)
		log.Trace(string(data))
		return data, nil
	}
	log.Infof("No file content found for %s in cache", filepath)
	p := path.Join(DataPath, filepath)
	log.Infof("Reading %s", p)
	c, _, err := g.Repositories.DownloadContents(ctx, DataRepoOwner, DataRepoName, p, nil)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	content, err := ioutil.ReadAll(c)
	if err != nil {
		return nil, err
	}
	if err := g.Cache.SetFile(ctx, filepath, content); err != nil {
		return nil, err
	}
	return content, nil
}

func (g *GithubClient) GetTmpCopy(ctx context.Context, prefix string, path string) (string, error) {
	tmpfile, err := ioutil.TempFile("", prefix)
	if err != nil {
		log.Fatal(err)
	}
	data, err := g.ReadFile(ctx, path)
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

//////////////////////////////////////////////////////////////////////////////
// FANTASY GAME
//////////////////////////////////////////////////////////////////////////////
const FantasyGameDirectory = "fantasy-game"
const FantasyGameFilename = FantasyGameDirectory
const FantasyGameExtension = "xml"

func YahooFantasyGameURL() string {
	return fmt.Sprintf("%s/game/nhl", BaseAPIURL)
}

func (g *GithubClient) FindFantasyGame(ctx context.Context) ([]*GithubFile, error) {
	return g.Find(ctx, FantasyGameDirectory, FantasyGameFilename, FantasyGameExtension)
}

func (g *GithubClient) CreateFantasyGame(ctx context.Context, content []byte) error {
	fn := GetGithubFilename(FantasyGameFilename, FantasyGameExtension, time.Now())
	return g.CreateFile(ctx, path.Join(FantasyGameDirectory, fn), "New fantasy game file", content)
}

//////////////////////////////////////////////////////////////////////////////
// League
//////////////////////////////////////////////////////////////////////////////
const LeagueDirectory = "league"
const LeagueFilename = LeagueDirectory
const LeagueExtension = "xml"

func YahooLeagueURL(leagueID int) string {
	return fmt.Sprintf("%s/league/%d.l.%d", BaseAPIURL, GameConst, leagueID)
}

func (g *GithubClient) FindLeague(ctx context.Context) ([]*GithubFile, error) {
	return g.Find(ctx, LeagueDirectory, LeagueFilename, LeagueExtension)
}

func (g *GithubClient) CreateLeague(ctx context.Context, content []byte) error {
	fn := GetGithubFilename(LeagueFilename, LeagueExtension, time.Now())
	return g.CreateFile(ctx, path.Join(LeagueDirectory, fn), "New league file", content)
}

//////////////////////////////////////////////////////////////////////////////
// Roster
//////////////////////////////////////////////////////////////////////////////
const RosterExtension = "xml"

func GetRosterFilename(teamID int, date time.Time) string {
	return fmt.Sprintf("roster-%02d-%4d-%02d-%02d", teamID, date.Year(), date.Month(), date.Day())
}

func GithubFindRoster(ctx context.Context, yfh *YFH, teamID int, date time.Time) ([]*GithubFile, error) {
	dir := GetTeamDir(teamID)
	fn := GetRosterFilename(teamID, date)
	return yfh.Github.Find(ctx, dir, fn, RosterExtension)
}

func YahooRosterURL(teamID int, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1/stats;type=season
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players", BaseAPIURL, GameConst, viper.GetInt(FlagLeagueID), teamID, date.Year(), date.Month(), date.Day())
}

func GithubCreateRoster(ctx context.Context, yfh *YFH, teamID int, date time.Time, content []byte) error {
	fn := GetGithubFilename(GetRosterFilename(teamID, date), RosterExtension, time.Now())
	p := path.Join(GetTeamDir(teamID), fn)
	msg := fmt.Sprintf("New roster file for team %d on %s", teamID, GetShortTimestamp(date))
	return yfh.Github.CreateFile(ctx, p, msg, content)
}

//////////////////////////////////////////////////////////////////////////////
// TEAM
//////////////////////////////////////////////////////////////////////////////

func YahooTeamURL(leagueID int, teamID int) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d", BaseAPIURL, GameConst, leagueID, teamID)
}

func GetTeamDir(teamID int) string {
	return fmt.Sprintf("team-%02d", teamID)
}

func GetTeamFilename(teamID int) string {
	return fmt.Sprintf("team-%02d", teamID)
}

const TeamExtension = "xml"

func (g *GithubClient) FindTeam(ctx context.Context, teamID int) ([]*GithubFile, error) {
	dir := GetTeamDir(teamID)
	fn := GetTeamFilename(teamID)
	return g.Find(ctx, dir, fn, TeamExtension)
}

func (g *GithubClient) CreateTeam(ctx context.Context, teamID int, content []byte) error {
	fn := GetGithubFilename(GetTeamFilename(teamID), TeamExtension, time.Now())
	p := path.Join(GetTeamDir(teamID), fn)
	msg := fmt.Sprintf("New file for team %d", teamID)
	return g.CreateFile(ctx, p, msg, content)
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

func (g *GithubClient) FindGamesList(ctx context.Context, date time.Time) ([]*GithubFile, error) {
	fn := GamesListFilename(date)
	return g.Find(ctx, GameDir(date), fn, GamesListExtension)
}

func (g *GithubClient) CreateGamesList(ctx context.Context, date time.Time, content []byte) error {
	fn := GetGithubFilename(GamesListFilename(date), GamesListExtension, time.Now())
	p := path.Join(GameDir(date), fn)
	msg := fmt.Sprintf("New file for games list %4d-%02d-%02d", date.Year(), date.Month(), date.Day())
	return g.CreateFile(ctx, p, msg, content)
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

func (g *GithubClient) FindGame(ctx context.Context, date time.Time, gameLink string) ([]*GithubFile, error) {
	log.Debugf("GithubClient.FindGame %s", gameLink)
	return g.Find(ctx, GameDir(date), gameLink, GameExtension)
}

func (g *GithubClient) CreateGame(ctx context.Context, date time.Time, gameLink string, content []byte) error {
	fn := GetGithubFilename(gameLink, GameExtension, time.Now())
	p := path.Join(GameDir(date), fn)
	msg := fmt.Sprintf("New file for game %s", gameLink)
	return g.CreateFile(ctx, p, msg, content)
}

//////////////////////////////////////////////////////////////////////////////
// Roster
//////////////////////////////////////////////////////////////////////////////
const RostersExtension = "xml"

func RostersDir(teamID int) string {
	return fmt.Sprintf("rosters/%02d", teamID)
}

func RosterPath(teamID int, date time.Time) string {
	return path.Join(RostersDir(teamID), RosterFilename(teamID, date))
}

func RosterFilename(teamID int, date time.Time) string {
	return fmt.Sprintf("rosters-%02d-%4d-%02d-%02d", teamID, date.Year(), date.Month(), date.Day())
}

func RosterURL(teamID int, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1/stats;type=season
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players", BaseAPIURL, GameConst, GetLeagueID(), teamID, date.Year(), date.Month(), date.Day())
}

func (g *GithubClient) FindRoster(ctx context.Context, teamID int, date time.Time) ([]*GithubFile, error) {
	return g.Find(ctx, RostersDir(teamID), RosterFilename(teamID, date), RostersExtension)
}

func (g *GithubClient) CreateRoster(ctx context.Context, teamID int, date time.Time, content []byte) error {
	fn := GetGithubFilename(RosterFilename(teamID, date), RostersExtension, time.Now())
	p := path.Join(RostersDir(teamID), fn)
	msg := fmt.Sprintf("New file for roster team %02d on %4d-%02d-%02d", teamID, date.Year(), date.Month(), date.Day())
	return g.CreateFile(ctx, p, msg, content)
}

/////////////////////////////

func (g *GithubClient) ParseXML(ctx context.Context, path string) (*xmlmodel.FantasyContent, error) {
	data, err := g.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	var fantasy xmlmodel.FantasyContent
	if err := xml.Unmarshal(data, &fantasy); err != nil {
		return nil, err
	}
	return &fantasy, nil
}
