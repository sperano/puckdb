package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// PlayByPlayRepo provides access to play-by-play data.
type PlayByPlayRepo struct {
	storage Storage
}

// NewPlayByPlayRepo creates a new PlayByPlayRepo.
func NewPlayByPlayRepo(storage Storage) *PlayByPlayRepo {
	return &PlayByPlayRepo{storage: storage}
}

// Get returns the parsed play-by-play for a game.
func (r *PlayByPlayRepo) Get(date time.Time, gameID nhl.GameID) (*nhl.PlayByPlay, error) {
	data, err := r.storage.Read(PlayByPlayPath(date, gameID))
	if err != nil {
		return nil, err
	}

	var pbp nhl.PlayByPlay
	if err := json.Unmarshal(data, &pbp); err != nil {
		return nil, fmt.Errorf("parse play-by-play %s: %w", gameID, err)
	}

	return &pbp, nil
}

// GetRaw returns the raw play-by-play data for a game.
func (r *PlayByPlayRepo) GetRaw(date time.Time, gameID nhl.GameID) ([]byte, error) {
	return r.storage.Read(PlayByPlayPath(date, gameID))
}

// Save stores play-by-play data for a game.
func (r *PlayByPlayRepo) Save(date time.Time, gameID nhl.GameID, data []byte) error {
	return r.storage.Write(PlayByPlayPath(date, gameID), data)
}

// Exists returns true if play-by-play data exists for the game.
func (r *PlayByPlayRepo) Exists(date time.Time, gameID nhl.GameID) bool {
	return r.storage.Exists(PlayByPlayPath(date, gameID))
}

// ShiftChartRepo provides access to shift chart data.
type ShiftChartRepo struct {
	storage Storage
}

// NewShiftChartRepo creates a new ShiftChartRepo.
func NewShiftChartRepo(storage Storage) *ShiftChartRepo {
	return &ShiftChartRepo{storage: storage}
}

// Get returns the parsed shift chart for a game.
func (r *ShiftChartRepo) Get(date time.Time, gameID nhl.GameID) (*nhl.ShiftChart, error) {
	data, err := r.storage.Read(ShiftChartPath(date, gameID))
	if err != nil {
		return nil, err
	}

	var chart nhl.ShiftChart
	if err := json.Unmarshal(data, &chart); err != nil {
		return nil, fmt.Errorf("parse shift chart %s: %w", gameID, err)
	}

	return &chart, nil
}

// GetRaw returns the raw shift chart data for a game.
func (r *ShiftChartRepo) GetRaw(date time.Time, gameID nhl.GameID) ([]byte, error) {
	return r.storage.Read(ShiftChartPath(date, gameID))
}

// Save stores shift chart data for a game.
func (r *ShiftChartRepo) Save(date time.Time, gameID nhl.GameID, data []byte) error {
	return r.storage.Write(ShiftChartPath(date, gameID), data)
}

// Exists returns true if shift chart data exists for the game.
func (r *ShiftChartRepo) Exists(date time.Time, gameID nhl.GameID) bool {
	return r.storage.Exists(ShiftChartPath(date, gameID))
}

// GameStoryRepo provides access to game story data.
type GameStoryRepo struct {
	storage Storage
}

// NewGameStoryRepo creates a new GameStoryRepo.
func NewGameStoryRepo(storage Storage) *GameStoryRepo {
	return &GameStoryRepo{storage: storage}
}

// Get returns the parsed game story for a game.
func (r *GameStoryRepo) Get(date time.Time, gameID nhl.GameID) (*nhl.GameStory, error) {
	data, err := r.storage.Read(GameStoryPath(date, gameID))
	if err != nil {
		return nil, err
	}

	var story nhl.GameStory
	if err := json.Unmarshal(data, &story); err != nil {
		return nil, fmt.Errorf("parse game story %s: %w", gameID, err)
	}

	return &story, nil
}

// GetRaw returns the raw game story data for a game.
func (r *GameStoryRepo) GetRaw(date time.Time, gameID nhl.GameID) ([]byte, error) {
	return r.storage.Read(GameStoryPath(date, gameID))
}

// Save stores game story data for a game.
func (r *GameStoryRepo) Save(date time.Time, gameID nhl.GameID, data []byte) error {
	return r.storage.Write(GameStoryPath(date, gameID), data)
}

// Exists returns true if game story data exists for the game.
func (r *GameStoryRepo) Exists(date time.Time, gameID nhl.GameID) bool {
	return r.storage.Exists(GameStoryPath(date, gameID))
}
