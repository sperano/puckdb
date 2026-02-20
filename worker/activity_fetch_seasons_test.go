package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/graph/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr(i int) *int {
	return &i
}

func TestFilterSeasons_NoFilters(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
		{ID: nhl.NewSeason(2024), StandingsStart: "2024-10-04", StandingsEnd: "2025-04-17"},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{})

	assert.Len(t, result, 3)
	assert.Equal(t, 2022, result[0].StartYear())
	assert.Equal(t, 2023, result[1].StartYear())
	assert.Equal(t, 2024, result[2].StartYear())
}

func TestFilterSeasons_StartFilter(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2020), StandingsStart: "2020-01-13", StandingsEnd: "2020-08-31"},
		{ID: nhl.NewSeason(2021), StandingsStart: "2021-01-13", StandingsEnd: "2021-05-19"},
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2022)})

	assert.Len(t, result, 2)
	assert.Equal(t, 2022, result[0].StartYear())
	assert.Equal(t, 2023, result[1].StartYear())
}

func TestFilterSeasons_EndFilter(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2020), StandingsStart: "2020-01-13", StandingsEnd: "2020-08-31"},
		{ID: nhl.NewSeason(2021), StandingsStart: "2021-01-13", StandingsEnd: "2021-05-19"},
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{EndSeason: ptr(2021)})

	assert.Len(t, result, 2)
	assert.Equal(t, 2020, result[0].StartYear())
	assert.Equal(t, 2021, result[1].StartYear())
}

func TestFilterSeasons_BothFilters(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2020), StandingsStart: "2020-01-13", StandingsEnd: "2020-08-31"},
		{ID: nhl.NewSeason(2021), StandingsStart: "2021-01-13", StandingsEnd: "2021-05-19"},
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{
		StartSeason: ptr(2021),
		EndSeason:   ptr(2022),
	})

	assert.Len(t, result, 2)
	assert.Equal(t, 2021, result[0].StartYear())
	assert.Equal(t, 2022, result[1].StartYear())
}

func TestFilterSeasons_EmptyResult(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2099)})

	assert.Empty(t, result)
}

func TestFilterSeasons_InvalidDates(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "invalid-date", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "invalid-date"},
		{ID: nhl.NewSeason(2024), StandingsStart: "2024-10-04", StandingsEnd: "2025-04-17"},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{})

	// Only the valid season should be included
	assert.Len(t, result, 1)
	assert.Equal(t, 2024, result[0].StartYear())
}

func TestFetchSeasonsData_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := &MockNHLClient{}

	apiSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
		{ID: nhl.NewSeason(2024), StandingsStart: "2024-10-04", StandingsEnd: "2025-04-17"},
	}
	client.On("SeasonStandingManifest", ctx).Return(apiSeasons, nil)

	result, err := fetchSeasonsDataImpl(ctx, client, &model.SeasonsInput{
		StartSeason: ptr(2023),
	})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, 2023, result[0].StartYear())
	assert.Equal(t, 2024, result[1].StartYear())
	client.AssertExpectations(t)
}

func TestFetchSeasonsData_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := &MockNHLClient{}

	expectedErr := errors.New("API connection failed")
	client.On("SeasonStandingManifest", ctx).Return(nil, expectedErr)

	result, err := fetchSeasonsDataImpl(ctx, client, &model.SeasonsInput{})

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
	client.AssertExpectations(t)
}

func TestFetchSeasonsData_EmptyResponse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := &MockNHLClient{}

	client.On("SeasonStandingManifest", ctx).Return([]nhl.SeasonInfo{}, nil)

	result, err := fetchSeasonsDataImpl(ctx, client, &model.SeasonsInput{})

	require.NoError(t, err)
	assert.Empty(t, result)
	client.AssertExpectations(t)
}

func TestSeasonInfo_Label(t *testing.T) {
	t.Parallel()

	tests := []struct {
		startYear int
		expected  string
	}{
		{2022, "2022-23"},
		{2023, "2023-24"},
		{2024, "2024-25"},
		{1999, "1999-00"}, // Edge case: century rollover
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			s := SeasonInfo{StartDate: time.Date(tt.startYear, 10, 1, 0, 0, 0, 0, time.UTC)}
			assert.Equal(t, tt.expected, s.Label())
		})
	}
}
