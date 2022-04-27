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
	flags.Int(FlagLeagueID, 0, "Yahoo league ID")
	viper.BindPFlag(FlagLeagueID, flags.Lookup(FlagLeagueID))

	flags.String(FlagTeamIDs, "", "List of team IDs, seperated by commas")
	viper.BindPFlag(FlagTeamIDs, flags.Lookup(FlagTeamIDs))

	flags.Int(FlagSeasonStartYear, 2021, "Season start year")
	viper.BindPFlag(FlagSeasonStartYear, flags.Lookup(FlagSeasonStartYear))
	flags.Int(FlagSeasonStartMonth, 10, "Season start month")
	viper.BindPFlag(FlagSeasonStartMonth, flags.Lookup(FlagSeasonStartMonth))
	flags.Int(FlagSeasonStartDay, 12, "Season start day")
	viper.BindPFlag(FlagSeasonStartDay, flags.Lookup(FlagSeasonStartDay))

	flags.Int(FlagSeasonEndYear, 2022, "Season end year")
	viper.BindPFlag(FlagSeasonEndYear, flags.Lookup(FlagSeasonEndYear))
	flags.Int(FlagSeasonEndMonth, 4, "Season end month")
	viper.BindPFlag(FlagSeasonEndMonth, flags.Lookup(FlagSeasonEndMonth))
	flags.Int(FlagSeasonEndDay, 29, "Season end day")
	viper.BindPFlag(FlagSeasonEndDay, flags.Lookup(FlagSeasonEndDay))

	flags.String(FlagYahooOAuth2ClientID, "", "Yahoo! OAuth2 Client ID")
	viper.BindPFlag(FlagYahooOAuth2ClientID, flags.Lookup(FlagYahooOAuth2ClientID))
	flags.String(FlagYahooOAuth2ClientSecret, "", "Yahoo! OAuth2 Client Secret")
	viper.BindPFlag(FlagYahooOAuth2ClientSecret, flags.Lookup(FlagYahooOAuth2ClientSecret))
	flags.String(FlagYahooOAuth2ClientRedirect, "", "Yahoo! Oauth2 Client Redirect")
	viper.BindPFlag(FlagYahooOAuth2ClientRedirect, flags.Lookup(FlagYahooOAuth2ClientRedirect))
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
