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

func EnsureNHL(db *gorm.DB) error {
	if err := newNHLConference(db, CONF_EASTERN, "Eastern"); err != nil {
		return err
	}
	if err := newNHLConference(db, CONF_WESTERN, "Western"); err != nil {
		return err
	}
	if err := newNHLDivision(db, DIV_ATLANTIC, CONF_EASTERN, "Atlantic"); err != nil {
		return err
	}
	if err := newNHLDivision(db, DIV_CENTRAL, CONF_WESTERN, "Central"); err != nil {
		return err
	}
	if err := newNHLDivision(db, DIV_PACIFIC, CONF_WESTERN, "Pacific"); err != nil {
		return err
	}
	if err := newNHLDivision(db, DIV_METROPOLITAN, CONF_EASTERN, "Metropolitan"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 1, CONF_EASTERN, DIV_ATLANTIC, "Boston", "Bruins"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 2, CONF_EASTERN, DIV_ATLANTIC, "Buffalo", "Sabres"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 3, CONF_WESTERN, DIV_PACIFIC, "Calgary", "Flames"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 4, CONF_WESTERN, DIV_CENTRAL, "Chicago", "Blackhawks"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 5, CONF_EASTERN, DIV_ATLANTIC, "Detroit", "Red Wings"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 6, CONF_WESTERN, DIV_PACIFIC, "Edmonton", "Oilers"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 7, CONF_EASTERN, DIV_METROPOLITAN, "Carolina", "Hurricanes"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 8, CONF_WESTERN, DIV_PACIFIC, "Los Angeles", "Kings"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 9, CONF_WESTERN, DIV_CENTRAL, "Dallas", "Stars"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 10, CONF_EASTERN, DIV_ATLANTIC, "Montreal", "Canadiens"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 11, CONF_EASTERN, DIV_METROPOLITAN, "New Jersey", "Devils"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 12, CONF_EASTERN, DIV_METROPOLITAN, "New York", "Islanders"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 13, CONF_EASTERN, DIV_METROPOLITAN, "New York", "Rangers"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 14, CONF_EASTERN, DIV_ATLANTIC, "Ottawa", "Senators"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 15, CONF_EASTERN, DIV_METROPOLITAN, "Philadelphia", "Flyers"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 16, CONF_EASTERN, DIV_METROPOLITAN, "Pittsburgh", "Penguins"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 17, CONF_WESTERN, DIV_CENTRAL, "Colorado", "Avalanche"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 18, CONF_WESTERN, DIV_PACIFIC, "San Jose", "Sharks"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 19, CONF_WESTERN, DIV_CENTRAL, "St. Louis", "Blues"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 20, CONF_EASTERN, DIV_ATLANTIC, "Tampa Bay", "Lightning"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 21, CONF_EASTERN, DIV_ATLANTIC, "Toronto", "Maple Leafs"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 22, CONF_WESTERN, DIV_PACIFIC, "Vancouver", "Canucks"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 23, CONF_EASTERN, DIV_METROPOLITAN, "Washington", "Capitals"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 24, CONF_WESTERN, DIV_CENTRAL, "Arizona", "Coyotes"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 25, CONF_WESTERN, DIV_PACIFIC, "Anaheim", "Ducks"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 26, CONF_EASTERN, DIV_ATLANTIC, "Florida", "Panthers"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 27, CONF_WESTERN, DIV_CENTRAL, "Nashville", "Predators"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 28, CONF_WESTERN, DIV_CENTRAL, "Winnipeg", "Jets"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 29, CONF_EASTERN, DIV_METROPOLITAN, "Columbus", "Blue Jackets"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 30, CONF_WESTERN, DIV_CENTRAL, "Minnesota", "Wild"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 58, CONF_WESTERN, DIV_PACIFIC, "Vegas", "Golden Knights"); err != nil {
		return err
	}
	if err := newNHLTeam(db, 59, CONF_WESTERN, DIV_PACIFIC, "Seattle", "Kraken"); err != nil {
		return err
	}
	return nil
}
