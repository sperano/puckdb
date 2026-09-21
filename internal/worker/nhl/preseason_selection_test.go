package nhl

import (
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/stretchr/testify/assert"
)

func TestFilterSeasons_SeptemberPreseasonSkipsOnlyNHLWork(t *testing.T) {
	t.Parallel()
	const requestedSeason = 2026
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	seasons := []nhlapi.SeasonInfo{
		{
			ID:             nhlapi.NewSeason(requestedSeason),
			StandingsStart: nhlapi.MustParseDate("2026-09-29"),
			StandingsEnd:   nhlapi.MustParseDate("2027-04-10"),
		},
	}

	selected := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(requestedSeason)}, now)

	assert.Empty(t, selected, "NHL work remains gated until the standings start date")
}
