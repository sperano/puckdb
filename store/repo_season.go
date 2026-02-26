package store

import (
	"encoding/json"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
)

// SeasonRepo provides access to NHL season data.
type SeasonRepo struct {
	storage Storage
}

// NewSeasonRepo creates a new SeasonRepo.
func NewSeasonRepo(storage Storage) *SeasonRepo {
	return &SeasonRepo{storage: storage}
}

// GetManifest returns the parsed seasons manifest.
func (r *SeasonRepo) GetManifest() ([]nhl.SeasonInfo, error) {
	data, err := r.storage.Read(SeasonsManifestPath())
	if err != nil {
		return nil, err
	}

	var seasons []nhl.SeasonInfo
	if err := json.Unmarshal(data, &seasons); err != nil {
		return nil, fmt.Errorf("parse seasons manifest: %w", err)
	}

	return seasons, nil
}

// GetManifestRaw returns the raw seasons manifest data.
func (r *SeasonRepo) GetManifestRaw() ([]byte, error) {
	return r.storage.Read(SeasonsManifestPath())
}

// SaveManifest stores seasons manifest data.
func (r *SeasonRepo) SaveManifest(data []byte) error {
	return r.storage.Write(SeasonsManifestPath(), data)
}

// ManifestExists returns true if seasons manifest data exists.
func (r *SeasonRepo) ManifestExists() bool {
	return r.storage.Exists(SeasonsManifestPath())
}

// GetStandings returns the parsed standings for a season.
func (r *SeasonRepo) GetStandings(seasonID int) ([]nhl.Standing, error) {
	data, err := r.storage.Read(SeasonStandingsPath(seasonID))
	if err != nil {
		return nil, err
	}

	var standings []nhl.Standing
	if err := json.Unmarshal(data, &standings); err != nil {
		return nil, fmt.Errorf("parse standings for season %d: %w", seasonID, err)
	}

	return standings, nil
}

// GetStandingsRaw returns the raw standings data for a season.
func (r *SeasonRepo) GetStandingsRaw(seasonID int) ([]byte, error) {
	return r.storage.Read(SeasonStandingsPath(seasonID))
}

// SaveStandings stores standings data for a season.
func (r *SeasonRepo) SaveStandings(seasonID int, data []byte) error {
	return r.storage.Write(SeasonStandingsPath(seasonID), data)
}

// StandingsExists returns true if standings data exists for a season.
func (r *SeasonRepo) StandingsExists(seasonID int) bool {
	return r.storage.Exists(SeasonStandingsPath(seasonID))
}
