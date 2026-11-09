package maurice

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTeamLister returns teams for one user and an error for the rest.
type fakeTeamLister struct {
	userID string
	teams  []UserTeam
	err    error
}

func (f *fakeTeamLister) ListUserTeams(_ context.Context, userID string) ([]UserTeam, error) {
	if userID != f.userID {
		return nil, nil
	}
	return f.teams, f.err
}

func TestChat_SystemPromptNamesConfiguredTeams(t *testing.T) {
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "answer"}}}
	cfg := testConfig
	cfg.Teams = &fakeTeamLister{userID: testUser, teams: []UserTeam{{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny"}}}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), cfg)

	_, err := svc.Chat(context.Background(), ask("who manages my team?"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	require.GreaterOrEqual(t, len(llmMock.requests), 1)
	assert.Contains(t, llmMock.requests[0].Messages[0].Content, "Les Peter Stastny (team 8, league 29249)")
}

func TestChat_NoTeamListerNamesNoTeam(t *testing.T) {
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "answer"}}}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), testConfig)

	_, err := svc.Chat(context.Background(), ask("hello"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	assert.NotContains(t, llmMock.requests[0].Messages[0].Content, "manages")
}

func TestChat_TeamListerErrorFailsTheTurn(t *testing.T) {
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "answer"}}}
	cfg := testConfig
	cfg.Teams = &fakeTeamLister{userID: testUser, err: errors.New("database unavailable")}
	svc := NewService(llmMock, newMockMCP(), newTestDB(t), cfg)

	_, err := svc.Chat(context.Background(), ask("hello"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "database unavailable")
}
