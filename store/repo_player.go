package store

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/sperano/nhl-api-go/nhl"
)

// PlayerRepo provides access to NHL player data.
type PlayerRepo struct {
	storage Storage
}

// NewPlayerRepo creates a new PlayerRepo.
func NewPlayerRepo(storage Storage) *PlayerRepo {
	return &PlayerRepo{storage: storage}
}

// GetLanding returns the parsed player landing page data.
func (r *PlayerRepo) GetLanding(playerID nhl.PlayerID) (*nhl.PlayerLanding, error) {
	data, err := r.storage.Read(PlayerLandingPath(playerID))
	if err != nil {
		return nil, err
	}

	var landing nhl.PlayerLanding
	if err := json.Unmarshal(data, &landing); err != nil {
		return nil, fmt.Errorf("parse player landing %d: %w", playerID, err)
	}

	return &landing, nil
}

// GetLandingRaw returns the raw player landing data.
func (r *PlayerRepo) GetLandingRaw(playerID nhl.PlayerID) ([]byte, error) {
	return r.storage.Read(PlayerLandingPath(playerID))
}

// SaveLanding stores player landing data.
func (r *PlayerRepo) SaveLanding(playerID nhl.PlayerID, data []byte) error {
	return r.storage.Write(PlayerLandingPath(playerID), data)
}

// LandingExists returns true if player landing data exists.
func (r *PlayerRepo) LandingExists(playerID nhl.PlayerID) bool {
	return r.storage.Exists(PlayerLandingPath(playerID))
}

// IsMissing returns true if the player is marked as missing (404 cached).
func (r *PlayerRepo) IsMissing(playerID nhl.PlayerID) bool {
	return r.storage.Exists(MissingPlayerLandingPath(playerID))
}

// GetMissing returns the missing player info (extracted from boxscores).
func (r *PlayerRepo) GetMissing(playerID nhl.PlayerID) (*MissingPlayerLandingData, error) {
	data, err := r.storage.Read(MissingPlayerLandingPath(playerID))
	if err != nil {
		return nil, err
	}

	var info MissingPlayerLandingData
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse missing player %d: %w", playerID, err)
	}

	return &info, nil
}

// MarkMissing saves a missing player marker with minimal info from boxscores.
func (r *PlayerRepo) MarkMissing(playerID nhl.PlayerID, info MissingPlayerLandingData) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal missing player info: %w", err)
	}
	return r.storage.Write(MissingPlayerLandingPath(playerID), data)
}

// ListLandings returns all player IDs that have landing data stored.
func (r *PlayerRepo) ListLandings() ([]nhl.PlayerID, error) {
	names, err := r.storage.List("players", "json")
	if err != nil {
		return nil, err
	}

	pattern := regexp.MustCompile(`^player-(\d+)-landing$`)
	var ids []nhl.PlayerID

	for _, name := range names {
		if m := pattern.FindStringSubmatch(name); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			ids = append(ids, nhl.PlayerID(id))
		}
	}

	return ids, nil
}

// GetGameLog returns the parsed player game log.
func (r *PlayerRepo) GetGameLog(playerID nhl.PlayerID, seasonID int, gameType int) (*nhl.PlayerGameLog, error) {
	data, err := r.storage.Read(PlayerGameLogPath(playerID, seasonID, gameType))
	if err != nil {
		return nil, err
	}

	var log nhl.PlayerGameLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("parse game log for player %d season %d: %w", playerID, seasonID, err)
	}

	return &log, nil
}

// GetGameLogRaw returns the raw player game log data.
func (r *PlayerRepo) GetGameLogRaw(playerID nhl.PlayerID, seasonID int, gameType int) ([]byte, error) {
	return r.storage.Read(PlayerGameLogPath(playerID, seasonID, gameType))
}

// SaveGameLog stores player game log data.
func (r *PlayerRepo) SaveGameLog(playerID nhl.PlayerID, seasonID int, gameType int, data []byte) error {
	return r.storage.Write(PlayerGameLogPath(playerID, seasonID, gameType), data)
}

// GameLogExists returns true if a player game log exists.
func (r *PlayerRepo) GameLogExists(playerID nhl.PlayerID, seasonID int, gameType int) bool {
	return r.storage.Exists(PlayerGameLogPath(playerID, seasonID, gameType))
}
