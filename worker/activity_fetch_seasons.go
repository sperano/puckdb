package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/store"
)

// SeasonInfo represents season metadata for Temporal serialization.
// This is a local copy of nhl.SeasonInfo to ensure proper serialization.
type SeasonInfo struct {
	// SeasonID is the unique season identifier (e.g., 20122013 for the 2012-13 season).
	// This is used for child workflow IDs because StartDate.Year() can be ambiguous
	// for lockout/pandemic seasons (e.g., 2012-13 started in Jan 2013, same as 2013-14).
	SeasonID  int       `json:"seasonId"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
}

// StartYear returns the start year of the season from the SeasonID.
// This is the canonical season identifier (e.g., 2012 for the 2012-13 season).
func (s SeasonInfo) StartYear() int {
	return s.SeasonID
}

// Label returns a display label for the season (e.g., "2024-25").
func (s SeasonInfo) Label() string {
	startYear := s.StartYear()
	endYearShort := (startYear + 1) % 100
	return fmt.Sprintf("%d-%02d", startYear, endYearShort)
}

// FetchSeasonsDataActivity fetches season data and filters by input range.
// It reads from the cached seasons manifest first (populated by DownloadSeasonsManifestActivity),
// falling back to the NHL API only if the cache doesn't exist.
func FetchSeasonsDataActivity(ctx context.Context, input *model.SeasonsInput) ([]SeasonInfo, error) {
	repos := store.NewDefaultRepos()

	// Try cache first
	if repos.Season.ManifestExists() {
		seasons, err := repos.Season.GetManifest()
		if err == nil {
			return filterSeasons(seasons, input), nil
		}
	}

	// Fall back to API
	client := nhl.NewClient()
	return fetchSeasonsDataImpl(ctx, client, input)
}

func fetchSeasonsDataImpl(ctx context.Context, client NHLClient, input *model.SeasonsInput) ([]SeasonInfo, error) {
	seasons, err := client.SeasonStandingManifest(ctx)
	if err != nil {
		return nil, err
	}
	return filterSeasons(seasons, input), nil
}

// filterSeasons converts nhl.SeasonInfo to worker.SeasonInfo and filters by input range.
func filterSeasons(seasons []nhl.SeasonInfo, input *model.SeasonsInput) []SeasonInfo {
	var result []SeasonInfo
	for _, s := range seasons {
		startYear := s.ID.StartYear()

		if input.StartSeason != nil && startYear < *input.StartSeason {
			continue
		}
		if input.EndSeason != nil && startYear > *input.EndSeason {
			continue
		}

		startDate, err := time.Parse(config.DateFormat, s.StandingsStart)
		if err != nil {
			continue
		}
		endDate, err := time.Parse(config.DateFormat, s.StandingsEnd)
		if err != nil {
			continue
		}

		result = append(result, SeasonInfo{
			SeasonID:  startYear,
			StartDate: startDate,
			EndDate:   endDate,
		})
	}

	return result
}
