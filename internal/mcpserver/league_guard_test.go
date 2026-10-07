package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	allowedLeagueID  int32 = 1001
	allowedLeagueKey       = "465.l.1001"
	otherLeagueID    int32 = 2002
	otherLeagueKey         = "465.l.2002"
	missingLeagueID  int32 = 3003
)

// fakeYahooQueries serves a fixed set of leagues. The embedded interface is
// nil, so a query a test did not stub panics instead of silently passing.
type fakeYahooQueries struct {
	yahooQueries
	leagues   []sqlcdb.YahooLeague
	lookupErr error
	lookups   int
	teamCalls []int32
	// teams overrides the single default team GetYahooTeamsByLeague returns.
	teams      []sqlcdb.YahooTeam
	managers   []sqlcdb.GetYahooTeamManagersByLeagueRow
	managerErr error
}

func newFakeYahooQueries() *fakeYahooQueries {
	return &fakeYahooQueries{leagues: []sqlcdb.YahooLeague{
		{ID: allowedLeagueID, LeagueKey: allowedLeagueKey, Name: "Allowed"},
		{ID: otherLeagueID, LeagueKey: otherLeagueKey, Name: "Other"},
	}}
}

func (f *fakeYahooQueries) GetYahooLeague(_ context.Context, id int32) (sqlcdb.YahooLeague, error) {
	f.lookups++
	if f.lookupErr != nil {
		return sqlcdb.YahooLeague{}, f.lookupErr
	}
	for _, league := range f.leagues {
		if league.ID == id {
			return league, nil
		}
	}
	return sqlcdb.YahooLeague{}, pgx.ErrNoRows
}

func (f *fakeYahooQueries) GetAllYahooLeagues(context.Context) ([]sqlcdb.YahooLeague, error) {
	return f.leagues, nil
}

func (f *fakeYahooQueries) GetYahooTeamsByLeague(_ context.Context, leagueID int32) ([]sqlcdb.YahooTeam, error) {
	f.teamCalls = append(f.teamCalls, leagueID)
	if f.teams != nil {
		return f.teams, nil
	}
	return []sqlcdb.YahooTeam{{LeagueID: leagueID, ID: 1, Name: "Team"}}, nil
}

func (f *fakeYahooQueries) GetYahooTeamManagersByLeague(context.Context, int32) ([]sqlcdb.GetYahooTeamManagersByLeagueRow, error) {
	return f.managers, f.managerErr
}

func TestLeagueGuardUnrestrictedAllowsEveryLeagueWithoutLookup(t *testing.T) {
	q := newFakeYahooQueries()
	guard := newLeagueGuard(q, nil)

	for _, id := range []int32{allowedLeagueID, otherLeagueID, missingLeagueID} {
		ok, err := guard.allow(context.Background(), id)
		require.NoError(t, err)
		assert.True(t, ok, "league %d", id)
	}
	assert.Zero(t, q.lookups)
	assert.Equal(t, q.leagues, guard.filter(q.leagues))
}

func TestLeagueGuardRestrictedAllowsOnlyListedKeys(t *testing.T) {
	q := newFakeYahooQueries()
	guard := newLeagueGuard(q, []string{allowedLeagueKey})

	tests := map[int32]bool{allowedLeagueID: true, otherLeagueID: false, missingLeagueID: false}
	for id, want := range tests {
		ok, err := guard.allow(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, want, ok, "league %d", id)
	}
	assert.Equal(t, len(tests), q.lookups, "every call is checked against the database")
}

func TestLeagueGuardRestrictedPropagatesLookupErrors(t *testing.T) {
	q := newFakeYahooQueries()
	q.lookupErr = errors.New("connection refused")
	guard := newLeagueGuard(q, []string{allowedLeagueKey})

	ok, err := guard.allow(context.Background(), allowedLeagueID)
	require.ErrorContains(t, err, "connection refused")
	assert.False(t, ok)
}

func TestLeagueGuardFilter(t *testing.T) {
	q := newFakeYahooQueries()
	guard := newLeagueGuard(q, []string{allowedLeagueKey, "465.l.9999"})

	got := guard.filter(q.leagues)
	require.Len(t, got, 1)
	assert.Equal(t, allowedLeagueKey, got[0].LeagueKey)
}

// TestLeagueGuardScopedRefusalMatchesMissingLeague pins that a client
// cannot tell a league outside the allowlist from one that does not exist.
func TestLeagueGuardScopedRefusalMatchesMissingLeague(t *testing.T) {
	q := newFakeYahooQueries()
	guard := newLeagueGuard(q, []string{allowedLeagueKey})
	handler := guard.scoped(func(context.Context, mcp.CallToolRequest, int32) (*mcp.CallToolResult, error) {
		t.Fatal("handler must not run for a refused league")
		return nil, nil
	})

	refused := callTool(t, handler, map[string]any{leagueIDArg: float64(otherLeagueID)})
	missing := callTool(t, handler, map[string]any{leagueIDArg: float64(missingLeagueID)})
	assert.True(t, refused.IsError)
	assert.Equal(t, "unknown league_id 2002", resultText(t, refused))
	assert.Equal(t, "unknown league_id 3003", resultText(t, missing))
}

func TestLeagueGuardScopedRunsHandlerForAllowedLeague(t *testing.T) {
	guard := newLeagueGuard(newFakeYahooQueries(), []string{allowedLeagueKey})
	var got int32
	handler := guard.scoped(func(_ context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
		got = leagueID
		return mcp.NewToolResultText("ok"), nil
	})

	result := callTool(t, handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	assert.False(t, result.IsError)
	assert.Equal(t, allowedLeagueID, got)
}

func TestLeagueGuardScopedRejectsBadLeagueID(t *testing.T) {
	guard := newLeagueGuard(newFakeYahooQueries(), nil)
	handler := guard.scoped(func(context.Context, mcp.CallToolRequest, int32) (*mcp.CallToolResult, error) {
		t.Fatal("handler must not run without a valid league_id")
		return nil, nil
	})

	tests := map[string]map[string]any{
		"league_id is required":        {},
		"invalid league_id -1":         {leagueIDArg: float64(-1)},
		"invalid league_id 4294968297": {leagueIDArg: float64(4294968297)},
	}
	for want, args := range tests {
		t.Run(want, func(t *testing.T) {
			assert.Equal(t, want, resultText(t, callTool(t, handler, args)))
		})
	}
}

func TestLeagueGuardScopedReportsLookupError(t *testing.T) {
	q := newFakeYahooQueries()
	q.lookupErr = errors.New("connection refused")
	guard := newLeagueGuard(q, []string{allowedLeagueKey})
	handler := guard.scoped(func(context.Context, mcp.CallToolRequest, int32) (*mcp.CallToolResult, error) {
		t.Fatal("handler must not run when the lookup fails")
		return nil, nil
	})

	result := callTool(t, handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	assert.True(t, result.IsError)
	assert.Equal(t, "connection refused", resultText(t, result))
}

func TestParseLeagueKeys(t *testing.T) {
	tests := map[string][]string{
		"":                              nil,
		" , ":                           nil,
		"465.l.1001":                    {"465.l.1001"},
		" 465.l.1001 , 453.l.2002,":     {"465.l.1001", "453.l.2002"},
		"465.l.1001,465.l.1001,nhl.l.7": {"465.l.1001", "nhl.l.7"},
	}
	for list, want := range tests {
		t.Run(list, func(t *testing.T) {
			got, err := ParseLeagueKeys(list)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestParseLeagueKeysRejects(t *testing.T) {
	tests := map[string]string{
		"1001":             `invalid league key "1001": want <game key>.l.<league ID>`,
		".l.1001":          `invalid league key ".l.1001"`,
		"465.l.":           `league ID "" is not a positive number`,
		"465.l.abc":        `league ID "abc" is not a positive number`,
		"465.l.0":          `league ID "0" is not a positive number`,
		"465.l.1001;465.l": `league ID "1001;465.l" is not a positive number`,
	}
	for list, want := range tests {
		t.Run(list, func(t *testing.T) {
			_, err := ParseLeagueKeys(list)
			require.ErrorContains(t, err, want)
		})
	}
}

// callTool runs handler with args the way the MCP server would.
func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}
