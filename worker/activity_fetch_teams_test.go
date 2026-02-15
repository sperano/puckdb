package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockTeamFetcher struct {
	calls  []TeamInfo
	errAt  int // Return error on this call index (-1 = never)
	err    error
}

func (m *mockTeamFetcher) FetchTeam(ctx context.Context, season, gameKey, leagueID, teamID int) error {
	m.calls = append(m.calls, TeamInfo{LeagueID: leagueID, TeamID: teamID})
	if m.errAt >= 0 && len(m.calls)-1 == m.errAt {
		return m.err
	}
	return nil
}

func TestFetchTeamsImpl_Success(t *testing.T) {
	ctx := context.Background()
	fetcher := &mockTeamFetcher{errAt: -1}
	teams := []TeamInfo{
		{LeagueID: 100, TeamID: 1},
		{LeagueID: 100, TeamID: 2},
		{LeagueID: 200, TeamID: 3},
	}

	err := fetchTeamsImpl(ctx, fetcher, 2024, 423, teams)

	assert.NoError(t, err)
	assert.Equal(t, teams, fetcher.calls)
}

func TestFetchTeamsImpl_EmptyTeams(t *testing.T) {
	ctx := context.Background()
	fetcher := &mockTeamFetcher{errAt: -1}

	err := fetchTeamsImpl(ctx, fetcher, 2024, 423, []TeamInfo{})

	assert.NoError(t, err)
	assert.Empty(t, fetcher.calls)
}

func TestFetchTeamsImpl_FetchError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("fetch failed")
	fetcher := &mockTeamFetcher{errAt: 1, err: expectedErr}
	teams := []TeamInfo{
		{LeagueID: 100, TeamID: 1},
		{LeagueID: 100, TeamID: 2},
		{LeagueID: 100, TeamID: 3},
	}

	err := fetchTeamsImpl(ctx, fetcher, 2024, 423, teams)

	assert.ErrorIs(t, err, expectedErr)
	assert.Len(t, fetcher.calls, 2) // Stopped after second team failed
}

func TestFetchTeamsImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetcher := &mockTeamFetcher{errAt: -1}
	teams := []TeamInfo{
		{LeagueID: 100, TeamID: 1},
	}

	err := fetchTeamsImpl(ctx, fetcher, 2024, 423, teams)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, fetcher.calls)
}
