package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCombinedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantFirstName string
		wantLastName  string
	}{
		{
			name:          "simple two part name",
			input:         "Connor McDavid",
			wantFirstName: "Connor",
			wantLastName:  "McDavid",
		},
		{
			name:          "hyphenated first name",
			input:         "Pierre-Luc Dubois",
			wantFirstName: "Pierre-Luc",
			wantLastName:  "Dubois",
		},
		{
			name:          "multi-part last name",
			input:         "James van Riemsdyk",
			wantFirstName: "James",
			wantLastName:  "van Riemsdyk",
		},
		{
			name:          "first name only",
			input:         "Cher",
			wantFirstName: "Cher",
			wantLastName:  "",
		},
		{
			name:          "empty string",
			input:         "",
			wantFirstName: "",
			wantLastName:  "",
		},
		{
			name:          "whitespace only",
			input:         "   ",
			wantFirstName: "",
			wantLastName:  "",
		},
		{
			name:          "leading/trailing whitespace",
			input:         "  Connor McDavid  ",
			wantFirstName: "Connor",
			wantLastName:  "McDavid",
		},
		{
			name:          "three part name",
			input:         "Jean-Gabriel Pageau",
			wantFirstName: "Jean-Gabriel",
			wantLastName:  "Pageau",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			firstName, lastName := parseCombinedName(tt.input)
			assert.Equal(t, tt.wantFirstName, firstName, "firstName mismatch")
			assert.Equal(t, tt.wantLastName, lastName, "lastName mismatch")
		})
	}
}

func TestExtractTeamPlayers_Empty(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense:  []nhl.SkaterStats{},
		Goalies:  []nhl.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Empty(t, players)
}

func TestExtractTeamPlayers_Forwards(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{
				PlayerID: nhl.PlayerID(8478402),
				Name:     nhl.LocalizedString{Default: "Connor McDavid"},
				Position: "C",
			},
			{
				PlayerID: nhl.PlayerID(8477934),
				Name:     nhl.LocalizedString{Default: "Leon Draisaitl"},
				Position: "C",
			},
		},
		Defense: []nhl.SkaterStats{},
		Goalies: []nhl.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 2)
	assert.Equal(t, int64(8478402), players[0].ID)
	assert.Equal(t, "Connor", players[0].FirstName)
	assert.Equal(t, "McDavid", players[0].LastName)
	assert.Equal(t, "C", players[0].Position)
	assert.Equal(t, int64(8477934), players[1].ID)
}

func TestExtractTeamPlayers_Defense(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense: []nhl.SkaterStats{
			{
				PlayerID: nhl.PlayerID(8480069),
				Name:     nhl.LocalizedString{Default: "Cale Makar"},
				Position: "D",
			},
		},
		Goalies: []nhl.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 1)
	assert.Equal(t, int64(8480069), players[0].ID)
	assert.Equal(t, "Cale", players[0].FirstName)
	assert.Equal(t, "Makar", players[0].LastName)
	assert.Equal(t, "D", players[0].Position)
}

func TestExtractTeamPlayers_Goalies(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense:  []nhl.SkaterStats{},
		Goalies: []nhl.GoalieStats{
			{
				PlayerID: nhl.PlayerID(8479394),
				Name:     nhl.LocalizedString{Default: "Connor Hellebuyck"},
				Position: "G",
			},
		},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 1)
	assert.Equal(t, int64(8479394), players[0].ID)
	assert.Equal(t, "Connor", players[0].FirstName)
	assert.Equal(t, "Hellebuyck", players[0].LastName)
	assert.Equal(t, "G", players[0].Position)
}

func TestExtractTeamPlayers_AllPositions(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{PlayerID: nhl.PlayerID(1), Name: nhl.LocalizedString{Default: "Forward One"}, Position: "LW"},
			{PlayerID: nhl.PlayerID(2), Name: nhl.LocalizedString{Default: "Forward Two"}, Position: "RW"},
		},
		Defense: []nhl.SkaterStats{
			{PlayerID: nhl.PlayerID(3), Name: nhl.LocalizedString{Default: "Defense One"}, Position: "D"},
		},
		Goalies: []nhl.GoalieStats{
			{PlayerID: nhl.PlayerID(4), Name: nhl.LocalizedString{Default: "Goalie One"}, Position: "G"},
		},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 4)
	// Verify order: forwards, then defense, then goalies
	assert.Equal(t, int64(1), players[0].ID)
	assert.Equal(t, int64(2), players[1].ID)
	assert.Equal(t, int64(3), players[2].ID)
	assert.Equal(t, int64(4), players[3].ID)
}

// --- extractBoxscoreDataForSeasonImpl tests ---

func TestExtractBoxscoreDataForSeason_EmptySeason(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Season where start > end (no days to process)
	season := SeasonInfo{
		StartDate: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	}

	extractor := func(ctx context.Context, day time.Time) ([]BoxscorePlayer, error) {
		t.Fatal("extractor should not be called for empty season")
		return nil, nil
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	require.NoError(t, err)
	assert.Empty(t, result.Players)
}

func TestExtractBoxscoreDataForSeason_SingleDay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	season := SeasonInfo{
		StartDate: day,
		EndDate:   day,
	}

	extractor := func(ctx context.Context, d time.Time) ([]BoxscorePlayer, error) {
		return []BoxscorePlayer{
			{ID: 1, FirstName: "Connor", LastName: "McDavid", Position: "C"},
			{ID: 2, FirstName: "Leon", LastName: "Draisaitl", Position: "C"},
		}, nil
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	require.NoError(t, err)
	assert.Len(t, result.Players, 2)
}

func TestExtractBoxscoreDataForSeason_Deduplication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	season := SeasonInfo{
		StartDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC), // 3 days
	}

	callCount := 0
	extractor := func(ctx context.Context, day time.Time) ([]BoxscorePlayer, error) {
		callCount++
		// Return same player each day - should be deduplicated
		return []BoxscorePlayer{
			{ID: 1, FirstName: "Connor", LastName: "McDavid", Position: "C"},
		}, nil
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	require.NoError(t, err)
	assert.Equal(t, 3, callCount)        // Called for each day
	assert.Len(t, result.Players, 1)     // Deduplicated to 1 player
	assert.Equal(t, int64(1), result.Players[0].ID)
}

func TestExtractBoxscoreDataForSeason_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	season := SeasonInfo{
		StartDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC),
	}

	callCount := 0
	extractor := func(ctx context.Context, day time.Time) ([]BoxscorePlayer, error) {
		callCount++
		if callCount == 2 {
			cancel() // Cancel after second day
		}
		return []BoxscorePlayer{{ID: int64(callCount)}}, nil
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Empty(t, result.Players)
}

func TestExtractBoxscoreDataForSeason_ExtractorError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	season := SeasonInfo{
		StartDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC), // 3 days
	}

	callCount := 0
	extractor := func(ctx context.Context, day time.Time) ([]BoxscorePlayer, error) {
		callCount++
		if callCount == 2 {
			return nil, errors.New("extraction failed")
		}
		return []BoxscorePlayer{{ID: int64(callCount)}}, nil
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	// Errors are logged but don't stop processing
	require.NoError(t, err)
	assert.Equal(t, 3, callCount) // All days processed
	assert.Len(t, result.Players, 2) // Day 1 and day 3 succeeded
}

func TestExtractBoxscoreDataForSeason_FutureEndDate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1).Truncate(24 * time.Hour)
	futureDate := now.AddDate(0, 0, 10) // 10 days in future

	season := SeasonInfo{
		StartDate: yesterday,
		EndDate:   futureDate, // Should be clamped to now
	}

	daysProcessed := 0
	extractor := func(ctx context.Context, day time.Time) ([]BoxscorePlayer, error) {
		daysProcessed++
		return nil, nil
	}

	_, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	require.NoError(t, err)
	// Should process yesterday and today (2 days), not 11 days
	assert.LessOrEqual(t, daysProcessed, 3) // Allow some buffer for timing
	assert.GreaterOrEqual(t, daysProcessed, 1)
}

func TestExtractBoxscoreDataForSeason_ProgressLogging(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create season with 35 days to trigger progress log at day 30
	season := SeasonInfo{
		StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 2, 4, 0, 0, 0, 0, time.UTC), // 35 days
	}

	daysProcessed := 0
	extractor := func(ctx context.Context, day time.Time) ([]BoxscorePlayer, error) {
		daysProcessed++
		return []BoxscorePlayer{{ID: int64(daysProcessed)}}, nil
	}

	result, err := extractBoxscoreDataForSeasonImpl(ctx, extractor, season)

	require.NoError(t, err)
	assert.Equal(t, 35, daysProcessed)
	assert.Len(t, result.Players, 35) // Each day returns unique player
}
