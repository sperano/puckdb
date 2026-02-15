package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchDay_NoTeams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs:   []TeamInfo{},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(nil)

	err := fetchDayImpl(ctx, fetcher, input)

	require.NoError(t, err)
	fetcher.AssertExpectations(t)
	fetcher.AssertNotCalled(t, "FetchRoster")
	fetcher.AssertNotCalled(t, "FetchTeamSummary")
}

func TestFetchDay_WithTeams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs: []TeamInfo{
			{LeagueID: 123, TeamID: 1},
			{LeagueID: 123, TeamID: 2},
		},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(nil)
	fetcher.On("FetchRoster", ctx, 123, 1, day).Return(nil)
	fetcher.On("FetchTeamSummary", ctx, 123, 1, day).Return(nil)
	fetcher.On("FetchRoster", ctx, 123, 2, day).Return(nil)
	fetcher.On("FetchTeamSummary", ctx, 123, 2, day).Return(nil)

	err := fetchDayImpl(ctx, fetcher, input)

	require.NoError(t, err)
	fetcher.AssertExpectations(t)
}

func TestFetchDay_DailyScheduleError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs:   []TeamInfo{{LeagueID: 123, TeamID: 1}},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(errors.New("schedule fetch failed"))

	err := fetchDayImpl(ctx, fetcher, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "schedule fetch failed")
	fetcher.AssertExpectations(t)
	fetcher.AssertNotCalled(t, "FetchRoster")
	fetcher.AssertNotCalled(t, "FetchTeamSummary")
}

func TestFetchDay_RosterError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs:   []TeamInfo{{LeagueID: 123, TeamID: 1}},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(nil)
	fetcher.On("FetchRoster", ctx, 123, 1, day).Return(errors.New("roster fetch failed"))

	err := fetchDayImpl(ctx, fetcher, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "roster fetch failed")
	fetcher.AssertExpectations(t)
	fetcher.AssertNotCalled(t, "FetchTeamSummary")
}

func TestFetchDay_TeamSummaryError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs:   []TeamInfo{{LeagueID: 123, TeamID: 1}},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(nil)
	fetcher.On("FetchRoster", ctx, 123, 1, day).Return(nil)
	fetcher.On("FetchTeamSummary", ctx, 123, 1, day).Return(errors.New("summary fetch failed"))

	err := fetchDayImpl(ctx, fetcher, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "summary fetch failed")
	fetcher.AssertExpectations(t)
}

func TestFetchDay_SecondTeamRosterError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs: []TeamInfo{
			{LeagueID: 123, TeamID: 1},
			{LeagueID: 123, TeamID: 2},
		},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(nil)
	fetcher.On("FetchRoster", ctx, 123, 1, day).Return(nil)
	fetcher.On("FetchTeamSummary", ctx, 123, 1, day).Return(nil)
	fetcher.On("FetchRoster", ctx, 123, 2, day).Return(errors.New("second roster failed"))

	err := fetchDayImpl(ctx, fetcher, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "second roster failed")
	fetcher.AssertExpectations(t)
}

func TestFetchDay_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	fetcher := &MockDayFetcher{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	input := &FetchDayInput{
		Day:       day,
		StartYear: 2023,
		TeamIDs: []TeamInfo{
			{LeagueID: 123, TeamID: 1},
		},
	}

	fetcher.On("FetchDailySchedule", ctx, day).Return(nil)
	cancel() // Cancel before team processing

	err := fetchDayImpl(ctx, fetcher, input)

	assert.ErrorIs(t, err, context.Canceled)
}
