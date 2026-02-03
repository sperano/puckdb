package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
)

// SeasonInfo represents season metadata for Temporal serialization.
// This is a local copy of nhl.SeasonInfo to ensure proper serialization.
type SeasonInfo struct {
	StartYear int       `json:"startYear"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
}

// Label returns a display label for the season (e.g., "2024-25").
func (s SeasonInfo) Label() string {
	endYearShort := (s.StartYear + 1) % 100
	return fmt.Sprintf("%d-%02d", s.StartYear, endYearShort)
}

// FetchSeasonsDataActivity fetches season data from the NHL API and filters by input range.
func FetchSeasonsDataActivity(ctx context.Context, input *model.DownloadSeasonsInput) ([]SeasonInfo, error) {
	client := nhl.NewClient()
	return fetchSeasonsDataImpl(ctx, client, input)
}

func fetchSeasonsDataImpl(ctx context.Context, client NHLClient, input *model.DownloadSeasonsInput) ([]SeasonInfo, error) {
	seasons, err := client.SeasonStandingManifest(ctx)
	if err != nil {
		return nil, err
	}

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
			StartYear: startYear,
			StartDate: startDate,
			EndDate:   endDate,
		})
	}

	return result, nil
}
