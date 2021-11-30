package core

import (
	"errors"
	"time"

	"github.com/ericsperano/yfh/core/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	config *Config
	orm    *gorm.DB
}

func NewDB(config *Config) (*DB, error) {
	dsn := "host=localhost user=yfh password=foo dbname=yfh port=5432 sslmode=disable TimeZone=America/Los_Angeles"
	orm, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	//orm, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	// Migrate the schema
	orm.AutoMigrate(&model.FantasyGame{})
	orm.AutoMigrate(&model.League{})
	orm.AutoMigrate(&model.Player{})
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
	/*
		var rplayer model.RosterPlayer
		if err := db.orm.Take(&rplayer, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
	*/
	return true, nil
}
