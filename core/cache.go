package core

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/ericsperano/yfh/core/xmlmodel"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yahoo"
)

const GameConst = 411
const BaseAPIURL = "https://fantasysports.yahooapis.com/fantasy/v2"
const OAuth2ClientID = "***REMOVED***"
const OAuth2ClientSecret = "***REMOVED***"

const TimetstampFormat = "20060102030405"

var ErrNoFileFound = errors.New("no file found")

func ParseTimestamp(timestamp string) (time.Time, error) {
	return time.Parse(TimetstampFormat, timestamp)
}

func GetTimestamp(time time.Time) string {
	return time.Format(TimetstampFormat)
}

type CachedFileInfo struct {
	Name string
	Time time.Time
}

func (c *CachedFileInfo) Filename() string {
	return fmt.Sprintf("%s_%s.xml", c.Name, GetTimestamp(c.Time))
}

type Cache struct {
	Config     *Config
	httpClient *http.Client
}

func createDirIfNotExists(path string, mode os.FileMode) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("mkdir %s\n", path)
		if err := os.MkdirAll(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func NewCache(config *Config) (*Cache, error) {
	if config.CachePath == "" {
		return nil, errors.New("CachePath is empty")
	}
	if err := createDirIfNotExists(config.CachePath, 0755); err != nil {
		return nil, err
	}
	cache := &Cache{
		Config: config,
	}
	for _, teamID := range config.TeamIDs {
		dir := path.Join(config.CachePath, cache.GetTeamDir(teamID))
		if err := createDirIfNotExists(dir, 0755); err != nil {
			return nil, err
		}
	}
	if err := createDirIfNotExists(path.Join(config.CachePath, "games"), 0755); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *Cache) HttpClient() (*http.Client, error) {
	if c.httpClient == nil {
		ctx := context.Background()
		conf := &oauth2.Config{
			ClientID:     OAuth2ClientID,
			ClientSecret: OAuth2ClientSecret,
			Scopes:       []string{"openid", "fspt-w"},
			Endpoint:     yahoo.Endpoint,
			RedirectURL:  "oob",
		}
		// Redirect user to consent page to ask for permission for the scopes specified above.
		url := conf.AuthCodeURL("state", oauth2.AccessTypeOnline)
		cmd := exec.Command("open", url)
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		rl, err := readline.New("Access code> ")
		if err != nil {
			return nil, err
		}
		defer rl.Close()
		line, err := rl.Readline()
		if err != nil {
			return nil, err
		}
		code := strings.TrimSuffix(line, "\n")
		tok, err := conf.Exchange(ctx, code)
		if err != nil {
			return nil, err
		}
		c.httpClient = conf.Client(ctx, tok)
	}
	return c.httpClient, nil
}

func (c *Cache) readdir(path string) ([]*CachedFileInfo, error) {
	fileinfos, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}
	cfis := []*CachedFileInfo{}
	for _, fileinfo := range fileinfos {
		if fileinfo.IsDir() {

		} else {
			name := fileinfo.Name()
			if strings.Contains(name, "_") {
				tokens := strings.Split(name, "_")
				if len(tokens) == 2 {
					name = tokens[0]
					tokens := strings.Split(tokens[1], ".")
					if len(tokens) == 2 {
						time, err := ParseTimestamp(tokens[0])
						if err != nil {
							return nil, err
						}
						cfi := &CachedFileInfo{
							Name: name, //fmt.Sprintf("%s.%s", name, tokens[1]),
							Time: time,
						}
						cfis = append(cfis, cfi)
					} else {
						log.Printf("More then two tokens in second _ token, ignoring: %s", name)
					}
				} else {
					log.Printf("More then two tokens in filename, ignoring: %s", name)
				}
			} else {
				log.Printf("No _ in filename, ignoring: %s", name)
			}
		}
	}
	return cfis, nil
}

func (c *Cache) find(dir string, name string) ([]*CachedFileInfo, error) {
	if len(dir) == 0 {
		dir = c.Config.CachePath
	} else {
		dir = path.Join(c.Config.CachePath, dir)
	}
	files := []*CachedFileInfo{}
	cfis, err := c.readdir(dir)
	if err != nil {
		return nil, err
	}
	for _, cfi := range cfis {
		if cfi.Name == name {
			files = append(files, cfi)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Time.Unix() > files[j].Time.Unix()
	})
	return files, nil
}

func (c *Cache) download(url string, dest string) (*CachedFileInfo, error) {
	cfi := CachedFileInfo{
		Name: dest + ".xml",
		Time: time.Now(),
	}
	client, err := c.HttpClient()
	if err != nil {
		return nil, err
	}
	filename := path.Join(c.Config.CachePath, fmt.Sprintf("%s_%s.xml", dest, GetTimestamp(time.Now())))
	fmt.Printf("Downloading %s into %s\n", url, filename)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = os.WriteFile(filename, body, 0644)
	if err != nil {
		return nil, err
	}
	return &cfi, nil
}

func (c *Cache) has(dir string, filename string) (bool, error) {
	cfis, err := c.find(dir, filename)
	if err != nil {
		return false, err
	}
	return len(cfis) > 0, nil
}

func (c *Cache) get(dir string, filename string) ([]byte, error) {
	cfis, err := c.find(dir, filename)
	if err != nil {
		return nil, err
	}
	if len(cfis) > 0 {
		cfi := cfis[0]
		var p string
		if len(dir) == 0 {
			p = path.Join(c.Config.CachePath, cfi.Filename())
		} else {
			p = path.Join(c.Config.CachePath, dir, cfi.Filename())
		}
		xmlFile, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer xmlFile.Close()
		data, err := ioutil.ReadAll(xmlFile)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, ErrNoFileFound
}

//////////////////////////////////////////////////////////////////////////////
// FANTASY GAME
//////////////////////////////////////////////////////////////////////////////
const FilenameFantasyGame = "fantasy-game"

func (c *Cache) HasFantasyGame() (bool, error) {
	return c.has("", FilenameFantasyGame)
}

func (c *Cache) FantasyGameURL() string {
	return fmt.Sprintf("%s/game/nhl", BaseAPIURL)
}

func (c *Cache) DownloadFantasyGame() (*CachedFileInfo, error) {
	return c.download(c.FantasyGameURL(), FilenameFantasyGame)
}

func (c *Cache) FindFantasyGame() ([]*CachedFileInfo, error) {
	return c.find("", FilenameFantasyGame)
}

func (c *Cache) GetFantasyGame() (*xmlmodel.FantasyGame, error) {
	data, err := c.get("", FilenameFantasyGame)
	if err != nil {
		return nil, err
	}
	var fantasy xmlmodel.FantasyContent
	xml.Unmarshal(data, &fantasy)
	return &fantasy.Game, nil
}

//////////////////////////////////////////////////////////////////////////////
// LEAGUE
//////////////////////////////////////////////////////////////////////////////
const FilenameLeague = "league"

func (c *Cache) HasLeague() (bool, error) {
	return c.has("", FilenameLeague)
}

func (c *Cache) LeagueURL() string {
	return fmt.Sprintf("%s/league/%d.l.%d", BaseAPIURL, GameConst, c.Config.LeagueID)
}

func (c *Cache) DownloadLeague() (*CachedFileInfo, error) {
	return c.download(c.LeagueURL(), FilenameLeague)
}

func (c *Cache) FindLeague() ([]*CachedFileInfo, error) {
	return c.find("", FilenameLeague)
}

func (c *Cache) GetLeague() (*xmlmodel.League, error) {
	data, err := c.get("", FilenameLeague)
	if err != nil {
		return nil, err
	}
	var fantasy xmlmodel.FantasyContent
	xml.Unmarshal(data, &fantasy)
	return &fantasy.League, nil
}

//////////////////////////////////////////////////////////////////////////////
// ROSTER
//////////////////////////////////////////////////////////////////////////////
func (c *Cache) GetTeamDir(teamID int) string {
	return fmt.Sprintf("team-%02d", teamID)
}

func (c *Cache) GetRosterFilename(teamID int, date time.Time) string {
	return fmt.Sprintf("roster-%02d-%2d-%02d-%02d", teamID, date.Year(), date.Month(), date.Day())
}

func (c *Cache) HasRoster(teamID int, date time.Time) (bool, error) {
	return c.has(c.GetTeamDir(teamID), c.GetRosterFilename(teamID, date))
}

func (c *Cache) RosterURL(teamID int, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1/stats;type=season
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players", BaseAPIURL, GameConst, c.Config.LeagueID, teamID, date.Year(), date.Month(), date.Day())
}

func (c *Cache) DownloadRoster(teamID int, date time.Time) (*CachedFileInfo, error) {
	p := path.Join(c.GetTeamDir(teamID), c.GetRosterFilename(teamID, date))
	return c.download(c.RosterURL(teamID, date), p)
}

func (c *Cache) FindRoster(teamID int, date time.Time) ([]*CachedFileInfo, error) {
	return c.find(c.GetTeamDir(teamID), c.GetRosterFilename(teamID, date))
}

func (c *Cache) GetRoster(teamID int, date time.Time) (*xmlmodel.Team, error) {
	data, err := c.get(c.GetTeamDir(teamID), c.GetRosterFilename(teamID, date))
	if err != nil {
		return nil, err
	}
	var fantasy xmlmodel.FantasyContent
	xml.Unmarshal(data, &fantasy)
	return &fantasy.Team, nil
}

//////////////////////////////////////////////////////////////////////////////
// Games List
//////////////////////////////////////////////////////////////////////////////
func (c *Cache) GetGamesListFilename(date time.Time) string {
	return fmt.Sprintf("games-list-%4d-%02d-%02d", date.Year(), date.Month(), date.Day())
}

func (c *Cache) HasGamesList(date time.Time) (bool, error) {
	return c.has("games", c.GetGamesListFilename(date))
}

func (c *Cache) GamesListURL(date time.Time) string {
	return fmt.Sprintf("https://sports.yahoo.com/nhl/scoreboard/?confId=&dateRange=%d-%d-%d&schedState=2", date.Year(), date.Month(), date.Day())
}

func (c *Cache) DownloadGamesList(date time.Time) (*CachedFileInfo, error) {
	p := path.Join("games", c.GetGamesListFilename(date))
	return c.download(c.GamesListURL(date), p)
}

func (c *Cache) FindGamesList(date time.Time) ([]*CachedFileInfo, error) {
	return c.find("games", c.GetGamesListFilename(date))
}

func (c *Cache) GetGamesList(date time.Time) (*xmlmodel.Team, error) {
	data, err := c.get("games", c.GetGamesListFilename(date))
	if err != nil {
		return nil, err
	}
	_ = data
	return nil, nil
}
