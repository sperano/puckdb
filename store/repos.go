package store

import (
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
)

// Repos provides a single entry point to all domain repositories.
// It aggregates all repository types and their underlying storage.
type Repos struct {
	// NHL repositories
	Schedule   *ScheduleRepo
	Boxscore   *BoxscoreRepo
	PlayByPlay *PlayByPlayRepo
	ShiftChart *ShiftChartRepo
	GameStory  *GameStoryRepo
	Player     *PlayerRepo
	Franchise  *FranchiseRepo
	Season     *SeasonRepo

	// Yahoo repositories
	Yahoo *YahooRepo

	// Underlying storage (exposed for advanced use cases)
	Storage Storage
}

// NewRepos creates a Repos instance backed by the provided storage.
func NewRepos(storage Storage) *Repos {
	return &Repos{
		Schedule:   NewScheduleRepo(storage),
		Boxscore:   NewBoxscoreRepo(storage),
		PlayByPlay: NewPlayByPlayRepo(storage),
		ShiftChart: NewShiftChartRepo(storage),
		GameStory:  NewGameStoryRepo(storage),
		Player:     NewPlayerRepo(storage),
		Franchise:  NewFranchiseRepo(storage),
		Season:     NewSeasonRepo(storage),
		Yahoo:      NewYahooRepo(storage),
		Storage:    storage,
	}
}

// NewFileRepos creates a Repos instance backed by filesystem storage.
func NewFileRepos(rootPath string) *Repos {
	return NewRepos(NewFSStorage(rootPath))
}

// NewMemRepos creates a Repos instance backed by in-memory storage.
// Primarily useful for testing.
func NewMemRepos() *Repos {
	return NewRepos(NewMemStorage())
}

// NewDefaultRepos creates a Repos instance using the configured data path.
// Uses instrumented storage for metrics collection.
// This is the standard way to create Repos in production code.
func NewDefaultRepos() *Repos {
	dataPath := viper.GetString(config.FlagDataPath)
	log.Trace().Str("path", dataPath).Msg("Initializing Repos")
	return NewRepos(NewInstrumentedStorage(NewFSStorage(dataPath)))
}
