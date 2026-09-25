package config

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

// ErrYahooNotConfigured signals that no Yahoo seasons config file is set.
// Callers use errors.Is to distinguish "Yahoo integration is off" from real
// failures (missing file, malformed YAML), which must not be silently swallowed.
var ErrYahooNotConfigured = errors.New("yahoo seasons config not set")

var (
	cachedSeasons YahooSeasonsMap
	seasonsOnce   sync.Once
	seasonsErr    error
)

// League and Season carry json tags because workflows record them in Temporal
// history (see shared.YahooSeasonsSnapshot); renaming a field would break
// replay of existing histories.
type League struct {
	LeagueID int   `yaml:"league_id" json:"leagueId"`
	TeamIDs  []int `yaml:"team_ids" json:"teamIds"`
	// TemporaryMetadataFrom is TEMPORARY: see LeagueMetadataSource. omitempty
	// keeps the recorded snapshot of every league without it unchanged.
	TemporaryMetadataFrom *LeagueMetadataSource `yaml:"temporary_metadata_from,omitempty" json:"temporaryMetadataFrom,omitempty"`
}

// LeagueMetadataSource names an earlier season's league whose cached Yahoo
// settings stand in for a league the Yahoo API cannot serve yet.
//
// TEMPORARY: Yahoo answers 403 "This application is not authorized to perform
// this action" for the 2026 leagues while the app's access request is pending.
// A league configured with temporary_metadata_from makes no Yahoo API calls;
// sync imports the source league's settings (scoring categories, roster
// positions, draft and waiver rules) under the league's own ID instead, and
// skips its teams, transactions, draft results, matchups and player pool. Once Yahoo serves
// the league again, drop temporary_metadata_from from the seasons config; the
// next import replaces the stand-in with the real settings. Then delete this
// type and its callers.
type LeagueMetadataSource struct {
	Season   int `yaml:"season" json:"season"`
	LeagueID int `yaml:"league_id" json:"leagueId"`
}

// UsesTemporaryMetadata reports whether the league imports stand-in settings
// from another season instead of calling the Yahoo API. TEMPORARY: see
// LeagueMetadataSource.
func (l League) UsesTemporaryMetadata() bool {
	return l.TemporaryMetadataFrom != nil
}

type Season struct {
	Leagues []League `yaml:"leagues" json:"leagues"`
}

func (s Season) GetLeague(leagueID int) (League, error) {
	for _, l := range s.Leagues {
		if l.LeagueID == leagueID {
			return l, nil
		}
	}
	return League{}, fmt.Errorf("league not found: %d", leagueID)
}

// YahooSeasonsMap is a map of seasons keyed by start year
type YahooSeasonsMap map[int]Season

func getYahooSeasons(path string) (YahooSeasonsMap, error) {
	log.Debug().Str("path", path).Msgf("Opening yahoo seasons config file")
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read yahoo seasons config file %s: %w", path, err)
	}
	var seasons YahooSeasonsMap
	err = yaml.Unmarshal(yamlFile, &seasons)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal yahoo seasons config file %s: %w", path, err)
	}
	if err := validateTemporaryMetadata(seasons); err != nil {
		return nil, fmt.Errorf("invalid yahoo seasons config file %s: %w", path, err)
	}
	return seasons, nil
}

// validateTemporaryMetadata rejects stand-in sources that cannot hold last
// season's settings: a missing league ID or a season that is not earlier.
func validateTemporaryMetadata(seasons YahooSeasonsMap) error {
	for year, season := range seasons {
		for _, league := range season.Leagues {
			source := league.TemporaryMetadataFrom
			if source == nil {
				continue
			}
			if source.LeagueID <= 0 || source.Season >= year {
				return fmt.Errorf("season %d league %d: temporary_metadata_from must name an earlier season "+
					"and a league ID, got season %d league %d", year, league.LeagueID, source.Season, source.LeagueID)
			}
		}
	}
	return nil
}

// GetYahooSeasonsConfig returns the Yahoo seasons map from the configured YAML
// file. Returns ErrYahooNotConfigured if no path is configured; a wrapped I/O
// or YAML error if the file is unreadable or malformed.
func GetYahooSeasonsConfig() (YahooSeasonsMap, error) {
	seasonsOnce.Do(func() {
		paramSeasons := viper.GetString(FlagYahooSeasons)
		if paramSeasons == "" {
			seasonsErr = ErrYahooNotConfigured
			return
		}
		log.Debug().Str("path", paramSeasons).Msg("Loading yahoo seasons config")
		cachedSeasons, seasonsErr = getYahooSeasons(paramSeasons)
	})
	return cachedSeasons, seasonsErr
}
