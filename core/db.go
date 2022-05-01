package core

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/ericsperano/yfh/core/model"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const FlagPostgresHost = "postgres_host"
const FlagPostgresUser = "postgres_user"
const FlagPostgresPassword = "postgres_password"
const FlagPostgresDatabase = "postgres_database"
const FlagPostgresPort = "postgres_port"
const FlagPostgresSSLMode = "postgres_ssl_mode"
const FlagPostgresTimeZone = "postgres_time_zone"

func SetupViperPostgres(flags *flag.FlagSet) {
	FString(flags, FlagPostgresHost, "localhost", "Postgres host")
	FString(flags, FlagPostgresUser, "yfh", "Postgres user")
	FString(flags, FlagPostgresPassword, "", "Postgres password")
	FString(flags, FlagPostgresDatabase, "yfh", "Postgres database")
	FInt(flags, FlagPostgresPort, 5432, "Postgres port")
	FString(flags, FlagPostgresSSLMode, "disable", "Postgres SSL mode")
	FString(flags, FlagPostgresTimeZone, "America/Los_Angeles", "Postgres time zone")
}

func GetDSN() string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s TimeZone=%s",
		viper.GetString(FlagPostgresHost),
		viper.GetString(FlagPostgresUser),
		viper.GetString(FlagPostgresPassword),
		viper.GetString(FlagPostgresDatabase),
		viper.GetInt(FlagPostgresPort),
		viper.GetString(FlagPostgresSSLMode),
		viper.GetString(FlagPostgresTimeZone))
}

func OpenGorm() (db *gorm.DB, err error) {
	dsn := GetDSN()
	log.Debugf("DSN: %s", dsn)
	return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}

func DoMigration(db *gorm.DB) error {
	log.Info("Starting database migration")
	if err := db.AutoMigrate(&model.Meta{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.NHLConference{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.NHLDivision{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.NHLTeam{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.FantasyGame{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.League{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.Player{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.PlayerStats{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.Game{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.Team{}); err != nil {
		return err
	}
	if err := db.AutoMigrate(&model.RosterPlayer{}); err != nil {
		return err
	}
	return nil
}

func DropEverything(db *gorm.DB) error {
	log.Info("Dropping all tables")
	migrator := db.Migrator()
	if err := migrator.DropTable(&model.NHLTeam{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.NHLDivision{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.NHLConference{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.FantasyGame{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.League{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.PlayerStats{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.Player{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.Game{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.Team{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.RosterPlayer{}); err != nil {
		return err
	}
	if err := migrator.DropTable(&model.Meta{}); err != nil {
		return err
	}
	return nil
}

func newNHLConference(db *gorm.DB, id uint, name string) error {
	conf := model.NHLConference{
		Model: gorm.Model{
			ID: id,
		},
		Name: name,
	}
	return conf.Ensure(db)
}

func newNHLDivision(db *gorm.DB, id uint, confID uint, name string) error {
	conf := model.NHLDivision{
		Model: gorm.Model{
			ID: id,
		},
		NHLConferenceID: confID,
		Name:            name,
	}
	return conf.Ensure(db)
}

func newNHLTeam(db *gorm.DB, id uint, confID uint, divisionID uint, city string, name string) error {
	team := model.NHLTeam{
		Model: gorm.Model{
			ID: id,
		},
		NHLConferenceID: confID,
		NHLDivisionID:   divisionID,
		City:            city,
		Name:            name,
	}
	return team.Ensure(db)
}

const CONF_EASTERN = 1
const CONF_WESTERN = 2
const DIV_ATLANTIC = 2
const DIV_CENTRAL = 3
const DIV_PACIFIC = 4
const DIV_METROPOLITAN = 7

func EnsureNHLConferences(db *gorm.DB) error {
	if err := newNHLConference(db, CONF_EASTERN, "Eastern"); err != nil {
		return err
	}
	return newNHLConference(db, CONF_WESTERN, "Western")
}

func EnsureNHLDivisions(db *gorm.DB) error {
	type nhldiv struct {
		ID           uint
		ConferenceID uint
		Name         string
	}
	divs := []nhldiv{
		{DIV_ATLANTIC, CONF_EASTERN, "Atlantic"},
		{DIV_METROPOLITAN, CONF_EASTERN, "Metropolitan"},
		{DIV_CENTRAL, CONF_WESTERN, "Central"},
		{DIV_PACIFIC, CONF_WESTERN, "Pacific"},
	}
	for _, div := range divs {
		if err := newNHLDivision(db, div.ID, div.ConferenceID, div.Name); err != nil {
			return err
		}
	}
	return nil
}

func EnsureNHLTeams(db *gorm.DB) error {
	type nhlteam struct {
		ID           uint
		ConferenceID uint
		DivisionID   uint
		City         string
		Name         string
	}
	teams := []nhlteam{
		{1, CONF_EASTERN, DIV_ATLANTIC, "Boston", "Bruins"},
		{2, CONF_EASTERN, DIV_ATLANTIC, "Buffalo", "Sabres"},
		{3, CONF_WESTERN, DIV_PACIFIC, "Calgary", "Flames"},
		{4, CONF_WESTERN, DIV_CENTRAL, "Chicago", "Blackhawks"},
		{5, CONF_EASTERN, DIV_ATLANTIC, "Detroit", "Red Wings"},
		{6, CONF_WESTERN, DIV_PACIFIC, "Edmonton", "Oilers"},
		{7, CONF_EASTERN, DIV_METROPOLITAN, "Carolina", "Hurricanes"},
		{8, CONF_WESTERN, DIV_PACIFIC, "Los Angeles", "Kings"},
		{9, CONF_WESTERN, DIV_CENTRAL, "Dallas", "Stars"},
		{10, CONF_EASTERN, DIV_ATLANTIC, "Montreal", "Canadiens"},
		{11, CONF_EASTERN, DIV_METROPOLITAN, "New Jersey", "Devils"},
		{12, CONF_EASTERN, DIV_METROPOLITAN, "New York", "Islanders"},
		{13, CONF_EASTERN, DIV_METROPOLITAN, "New York", "Rangers"},
		{14, CONF_EASTERN, DIV_ATLANTIC, "Ottawa", "Senators"},
		{15, CONF_EASTERN, DIV_METROPOLITAN, "Philadelphia", "Flyers"},
		{16, CONF_EASTERN, DIV_METROPOLITAN, "Pittsburgh", "Penguins"},
		{17, CONF_WESTERN, DIV_CENTRAL, "Colorado", "Avalanche"},
		{18, CONF_WESTERN, DIV_PACIFIC, "San Jose", "Sharks"},
		{19, CONF_WESTERN, DIV_CENTRAL, "St. Louis", "Blues"},
		{20, CONF_EASTERN, DIV_ATLANTIC, "Tampa Bay", "Lightning"},
		{21, CONF_EASTERN, DIV_ATLANTIC, "Toronto", "Maple Leafs"},
		{22, CONF_WESTERN, DIV_PACIFIC, "Vancouver", "Canucks"},
		{23, CONF_EASTERN, DIV_METROPOLITAN, "Washington", "Capitals"},
		{24, CONF_WESTERN, DIV_CENTRAL, "Arizona", "Coyotes"},
		{25, CONF_WESTERN, DIV_PACIFIC, "Anaheim", "Ducks"},
		{26, CONF_EASTERN, DIV_ATLANTIC, "Florida", "Panthers"},
		{27, CONF_WESTERN, DIV_CENTRAL, "Nashville", "Predators"},
		{28, CONF_WESTERN, DIV_CENTRAL, "Winnipeg", "Jets"},
		{29, CONF_EASTERN, DIV_METROPOLITAN, "Columbus", "Blue Jackets"},
		{30, CONF_WESTERN, DIV_CENTRAL, "Minnesota", "Wild"},
		{58, CONF_WESTERN, DIV_PACIFIC, "Vegas", "Golden Knights"},
		{59, CONF_WESTERN, DIV_PACIFIC, "Seattle", "Kraken"},
	}
	for _, team := range teams {
		if err := newNHLTeam(db, team.ID, team.ConferenceID, team.DivisionID, team.City, team.Name); err != nil {
			return err
		}
	}
	return nil
}

func EnsureNHL(db *gorm.DB) error {
	if err := EnsureNHLConferences(db); err != nil {
		return err
	}
	if err := EnsureNHLDivisions(db); err != nil {
		return err
	}
	return EnsureNHLTeams(db)
}
