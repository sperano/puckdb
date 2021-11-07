package core

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yahoo"
)

const GameConst = 411
const BaseAPIURL = "https://fantasysports.yahooapis.com/fantasy/v2/"
const OAuth2ClientID = "***REMOVED***"
const OAuth2ClientSecret = "***REMOVED***"
const GameNHLFilename = "game-nhl.xml"

func ParseTimestamp(timestamp string) (time.Time, error) {
	i, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return time.Now(), err
	}
	return time.Unix(i, 0), nil
}

/*
type DownloadedTeamForDateData struct {
	Team      int
	Year      int
	Month     int
	Day       int
	Timestamp time.Time
}

func NewDownloadTeamForDateData(team int, year int, month int, day int) *DownloadedTeamForDateData {
	return &DownloadedTeamForDateData{
		Team:      team,
		Year:      year,
		Month:     month,
		Day:       day,
		Timestamp: time.Now(),
	}
}
*/

/*
func (d *DownloadedTeamForDateData) Dir(config *core.Config) string {
	return path.Join(config.CachePath, strconv.Itoa(d.Year), strconv.Itoa(d.Month), strconv.Itoa(d.Day))
}

func (d *DownloadedTeamForDateData) Path(config *core.Config) string {
	return path.Join(d.Dir(config), fmt.Sprintf("%d-%d.html", d.Team, time.Now().Unix()))
}

*/

type CachedFileInfo struct {
	Name string
	Time time.Time
}

type Cache struct {
	Config     *Config
	httpClient *http.Client
}

func NewCache(config *Config) (*Cache, error) {
	if config.CachePath == "" {
		return nil, errors.New("CachePath is empty")
	}
	if _, err := os.Stat(config.CachePath); os.IsNotExist(err) {
		fmt.Printf("Creating CachePath: %s\n", config.CachePath)
		if err := os.MkdirAll(config.CachePath, 0755); err != nil {
			return nil, err
		}
	} else {
		fmt.Printf("CachePath already exists: %s\n", config.CachePath)
	}
	//d := time.Date(2021, 11, 02, 12, 41, 0, 0, time.UTC)
	//fmt.Printf("d=%+v\n", d)
	return &Cache{
		Config: config,
	}, nil
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

func (c *Cache) ReadDir(path string) ([]*CachedFileInfo, error) {
	fileinfos, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}
	cfis := []*CachedFileInfo{}
	for _, fileinfo := range fileinfos {
		if fileinfo.IsDir() {

		} else {
			//fmt.Printf("YO! %s\n", fileinfo.Name())
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
							Name: fmt.Sprintf("%s.%s", name, tokens[1]),
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

func (c *Cache) FindFile(name string) ([]*CachedFileInfo, error) {
	files := []*CachedFileInfo{}
	cfis, err := c.ReadDir(c.Config.CachePath)
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

func (c *Cache) DownloadFile(url string, dest string) (*CachedFileInfo, error) {
	tokens := strings.Split(dest, ".")
	if len(tokens) != 2 {
		return nil, fmt.Errorf("%d tokens in download filename", len(tokens))
	}
	cfi := CachedFileInfo{
		Name: fmt.Sprintf("%s.%s", tokens[0], tokens[1]),
		Time: time.Now(),
	}
	client, err := c.HttpClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("%s_%d.%s", tokens[0], time.Now().Unix(), tokens[1])
	err = os.WriteFile(path.Join(c.Config.CachePath, filename), body, 0644)
	if err != nil {
		return nil, err
	}
	return &cfi, nil
}

func (c *Cache) FindLeague() ([]*CachedFileInfo, error) {
	return c.FindFile("league.xml")
}

func (c *Cache) FindGameNHL() ([]*CachedFileInfo, error) {
	return c.FindFile(GameNHLFilename)
}

func (c *Cache) FindTeam(teamID int) ([]*CachedFileInfo, error) {
	return c.FindFile(fmt.Sprintf("team-%d.xml", teamID))
}

func (c *Cache) LeagueURL() string {
	return fmt.Sprintf("%s/league/%d.l.%d", BaseAPIURL, GameConst, c.Config.LeagueID)
}

func (c *Cache) GameNHLURL() string {
	return fmt.Sprintf("%s/game/nhl", BaseAPIURL)
}

func (c *Cache) TeamURL(teamID int) string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d", BaseAPIURL, GameConst, c.Config.LeagueID, teamID)

}

func (c *Cache) DownloadLeague() error {
	_, err := c.DownloadFile(c.LeagueURL(), "league.xml")
	return err
}

func (c *Cache) DownloadGameNHL() error {
	_, err := c.DownloadFile(c.GameNHLURL(), GameNHLFilename)
	return err
}

func (c *Cache) DownloadTeam(teamID int) error {
	filename := fmt.Sprintf("team-%d.xml", teamID)
	_, err := c.DownloadFile(c.TeamURL(teamID), filename)
	return err
}

/*
func (fsm *Cache) GetDownloads(team int, year int, month int, day int) []*DownloadedTeamForDateData {
	downloads := []*DownloadedTeamForDateData{}
	return downloads
}

func (fsm *Cache) DownloadTeamForDate(team int, year int, month int, day int) (*DownloadedTeamForDateData, error) {
	data := NewDownloadTeamForDateData(team, year, month, day)
	dir := data.Dir(fsm.Config)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fmt.Printf("Creating CachePath: %s\n", dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	} else {
		fmt.Printf("CachePath already exists: %s\n", dir)
	}
	return data, nil
}

func (fsm *FSManager) Ensure() error {
	//day := time.Date(fsm.Config.SeasonStartYear, fsm.Config.SeasonStartMonth, fsm.Config.SeasonStartDay, 0, 0, 0, 0, time.UTC)
	//now := time.Now()
	for {
		break
		/*
			team := 0
			for {
				team += 1
				fmt.Printf"Checking team %d on date: %v\n", team + 1, day)
				if team := fsm.Config.TotalTeams {
					break
				}
			}
			for team := 0; team < fsm.Config.TotalTeams; team++ {
				dlds := fsm.GetDownloads(team+1, day.Year(), int(day.Month()), day.Day())
				if len(dlds) == 0 {

				} else {
					fmt.Printf("CachePath: %s \n")
				}
			}

			day := day.AddDate(0, 0, 1)
			if day.Unix() > now.Unix() {
				break
			}
		*
	}
	return nil
}
*/
