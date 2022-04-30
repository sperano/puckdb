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

/*

func (db *DB) Create(value interface{}) (tx *gorm.DB) {
	return db.orm.Create(value)
}

func (db *DB) HasFantasyGame() (bool, error) {
	var fantasyGame model.FantasyGame
	if err := db.orm.First(&fantasyGame).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (db *DB) HasLeague() (bool, error) {
	var league model.League
	if err := db.orm.Take(&league).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (db *DB) HasPlayer(id int) (bool, error) {
	var player model.Player
	if err := db.orm.Take(&player, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (db *DB) HasRosterPlayer(teamID int, playerID int, month time.Month, day int) (bool, error) {
	var rplayers []model.RosterPlayer
	if err := db.orm.Where("team_id=? AND player_id=? AND month=? AND day=?", teamID, playerID, month, day).Find(&rplayers).Error; err != nil {
		return false, err
	}
	return len(rplayers) > 0, nil
}

func (db *DB) HasTeam(id int) (bool, error) {
	var team model.Team
	if err := db.orm.Take(&team, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
*/
