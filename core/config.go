package core

import (
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const GameConst = 411

const FlagLeagueID = "league_id"
const FlagTeamIDs = "team_ids"
const FlagSeasonStartYear = "season_start_year"
const FlagSeasonStartMonth = "season_start_month"
const FlagSeasonStartDay = "season_start_day"
const FlagSeasonEndYear = "season_end_year"
const FlagSeasonEndMonth = "season_end_month"
const FlagSeasonEndDay = "season_end_day"

func SetupViperConfig(flags *flag.FlagSet) {
	FInt(flags, FlagLeagueID, 0, "Yahoo league ID")
	FString(flags, FlagTeamIDs, "", "List of team IDs, seperated by commas")
	FInt(flags, FlagSeasonStartYear, 2021, "Season start year")
	FInt(flags, FlagSeasonStartMonth, 10, "Season start month")
	FInt(flags, FlagSeasonStartDay, 12, "Season start day")
	FInt(flags, FlagSeasonEndYear, 2022, "Season end year")
	FInt(flags, FlagSeasonEndMonth, 5, "Season end month")
	FInt(flags, FlagSeasonEndDay, 1, "Season end day")
	FString(flags, FlagYahooOAuth2ClientID, "", "Yahoo! OAuth2 Client ID")
	FString(flags, FlagYahooOAuth2ClientSecret, "", "Yahoo! OAuth2 Client Secret")
	FString(flags, FlagYahooOAuth2ClientRedirect, "", "Yahoo! Oauth2 Client Redirect")
}

func GetLeagueID() int {
	return viper.GetInt(FlagLeagueID)
}

func GetTeamIds() []int {
	ids := []int{}
	for _, tok := range strings.Split(viper.GetString(FlagTeamIDs), ",") {
		i, err := strconv.Atoi(tok)
		if err != nil {
			log.Fatal(err)
		}
		ids = append(ids, i)
	}
	return ids
}

func GetSeasonStart() time.Time {
	y := viper.GetInt(FlagSeasonStartYear)
	m := time.Month(viper.GetInt(FlagSeasonStartMonth))
	d := viper.GetInt(FlagSeasonStartDay)
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func GetSeasonEnd() time.Time {
	y := viper.GetInt(FlagSeasonEndYear)
	m := time.Month(viper.GetInt(FlagSeasonEndMonth))
	d := viper.GetInt(FlagSeasonEndDay)
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

/*
type Config struct {
	LeagueID         int        `yaml:"league_id"`
	CachePath        string     `yaml:"cache_path"`
	SeasonBeginYear  int        `yaml:"season_begin_year"`
	SeasonBeginMonth time.Month `yaml:"season_begin_month"`
	SeasonBeginDay   int        `yaml:"season_begin_day"`
	TeamIDs          []int      `yaml:"team_ids"`
	//Database         ConfigDatabase `yaml:"db"`
}
*/
/*
import (
	"io/ioutil"
	"log"
	"time"

	"gopkg.in/yaml.v2"
)

type ConfigDatabase struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Timezone string `yaml:"timezone"`
	SSLMode  string `yaml:"ssl_mode"`
}


var config Config

func GetConfig(path string) *Config {
	if config.LeagueID == 0 {
		yamlFile, err := ioutil.ReadFile(path)
		if err != nil {
			log.Fatalln(err)
		}
		err = yaml.Unmarshal(yamlFile, &config)
		if err != nil {
			log.Fatalln(err)
		}
	}
	return &config
}

*/
