package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/ericsperano/yfh/core/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	config *Config
	orm    *gorm.DB
}

func GetDSN(config *ConfigDatabase) string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=%s TimeZone=%s",
		config.Host, config.User, config.Password, config.Name, config.Port, config.SSLMode, config.Timezone)
}

func NewDB(config *Config) (*DB, error) {
	orm, err := gorm.Open(postgres.Open(GetDSN(&config.Database)), &gorm.Config{})
	//orm, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	// Migrate the schema
	orm.AutoMigrate(&model.FantasyGame{})
	orm.AutoMigrate(&model.League{})
	orm.AutoMigrate(&model.Player{})
	orm.AutoMigrate(&model.Team{})
	orm.AutoMigrate(&model.RosterPlayer{})
	db := DB{
		config: config,
		orm:    orm,
	}
	return &db, nil
}

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
	//if len(rplayers) > 0 {
	//	fmt.Printf("%+v\n", rplayers[0])
	//}
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
