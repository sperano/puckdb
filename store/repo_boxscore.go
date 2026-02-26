package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// BoxscoreRepo provides access to boxscore data.
type BoxscoreRepo struct {
	storage Storage
}

// NewBoxscoreRepo creates a new BoxscoreRepo.
func NewBoxscoreRepo(storage Storage) *BoxscoreRepo {
	return &BoxscoreRepo{storage: storage}
}

// Get returns the parsed boxscore for a game.
func (r *BoxscoreRepo) Get(date time.Time, gameID nhl.GameID) (*nhl.Boxscore, error) {
	data, err := r.storage.Read(BoxscorePath(date, gameID))
	if err != nil {
		return nil, err
	}

	var boxscore nhl.Boxscore
	if err := json.Unmarshal(data, &boxscore); err != nil {
		return nil, fmt.Errorf("parse boxscore %s: %w", gameID, err)
	}

	return &boxscore, nil
}

// GetRaw returns the raw boxscore data for a game.
func (r *BoxscoreRepo) GetRaw(date time.Time, gameID nhl.GameID) ([]byte, error) {
	return r.storage.Read(BoxscorePath(date, gameID))
}

// Save stores boxscore data for a game.
func (r *BoxscoreRepo) Save(date time.Time, gameID nhl.GameID, data []byte) error {
	return r.storage.Write(BoxscorePath(date, gameID), data)
}

// Exists returns true if a boxscore exists for the game.
func (r *BoxscoreRepo) Exists(date time.Time, gameID nhl.GameID) bool {
	return r.storage.Exists(BoxscorePath(date, gameID))
}
