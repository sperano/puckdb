package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ownershipTestPlayerID    int32 = 456
	maxInt32ForOwnershipTest       = 1<<31 - 1
)

type ownershipYahooQueries struct {
	*fakeYahooQueries
	rows      []sqlcdb.YahooTeamRoster
	queryArgs []sqlcdb.GetYahooTeamRostersByPlayerParams
	queryErr  error
}

func (q *ownershipYahooQueries) GetYahooTeamRostersByPlayer(_ context.Context, arg sqlcdb.GetYahooTeamRostersByPlayerParams) ([]sqlcdb.YahooTeamRoster, error) {
	q.queryArgs = append(q.queryArgs, arg)
	return q.rows, q.queryErr
}

func TestYahooPlayerOwnershipCompactsConsecutiveDays(t *testing.T) {
	q := &ownershipYahooQueries{fakeYahooQueries: newFakeYahooQueries(), rows: []sqlcdb.YahooTeamRoster{
		ownershipRoster(t, 3, "2025-10-01"),
		ownershipRoster(t, 3, "2025-10-02"),
		ownershipRoster(t, 3, "2025-10-04"),
		ownershipRoster(t, 7, "2025-10-05"),
		ownershipRoster(t, 7, "2025-10-06"),
	}}
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_player_ownership")
	result := callTool(t, tool.Handler, map[string]any{
		leagueIDArg:      float64(allowedLeagueID),
		yahooPlayerIDArg: float64(ownershipTestPlayerID),
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []sqlcdb.GetYahooTeamRostersByPlayerParams{{LeagueID: allowedLeagueID, PlayerID: ownershipTestPlayerID}}, q.queryArgs)
	assert.Equal(t, [][]string{
		{"team_id", "start_date", "end_date"},
		{"3", "2025-10-01", "2025-10-02"},
		{"3", "2025-10-04", "2025-10-04"},
		{"7", "2025-10-05", "2025-10-06"},
	}, parseCSV(t, resultText(t, result)))
}

func TestYahooPlayerOwnershipValidatesPlayerIDBeforeQuery(t *testing.T) {
	q := &ownershipYahooQueries{fakeYahooQueries: newFakeYahooQueries()}
	tool := yahooTestServer(q, nil).GetTool("get_yahoo_player_ownership")
	base := map[string]any{leagueIDArg: float64(allowedLeagueID)}
	for name, value := range map[string]any{
		"missing":    nil,
		"zero":       float64(0),
		"negative":   float64(-1),
		"fractional": float64(ownershipTestPlayerID) + 0.5,
		"overflow":   float64(maxInt32ForOwnershipTest + 1),
	} {
		t.Run(name, func(t *testing.T) {
			args := map[string]any{leagueIDArg: base[leagueIDArg]}
			if value != nil {
				args[yahooPlayerIDArg] = value
			}
			result := callTool(t, tool.Handler, args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), yahooPlayerIDArg)
			assert.Empty(t, q.queryArgs)
		})
	}
}

func TestYahooPlayerOwnershipReportsQueryError(t *testing.T) {
	q := &ownershipYahooQueries{fakeYahooQueries: newFakeYahooQueries(), queryErr: errors.New("connection refused")}
	tool := yahooTestServer(q, nil).GetTool("get_yahoo_player_ownership")
	result := callTool(t, tool.Handler, ownershipArgs())
	assert.True(t, result.IsError)
	assert.Equal(t, "connection refused", resultText(t, result))
}

func TestYahooPlayerOwnershipCapsOutputRanges(t *testing.T) {
	rows := make([]sqlcdb.YahooTeamRoster, maxYahooPlayerOwnershipRanges+1)
	for i := range rows {
		rows[i] = ownershipRoster(t, int32(i+1), "2025-10-01")
		rows[i].Date.Time = rows[i].Date.Time.AddDate(0, 0, i)
	}
	got := compactYahooPlayerOwnership(rows)
	assert.Len(t, got, maxYahooPlayerOwnershipRanges)
}

func ownershipArgs() map[string]any {
	return map[string]any{leagueIDArg: float64(allowedLeagueID), yahooPlayerIDArg: float64(ownershipTestPlayerID)}
}

func ownershipRoster(t *testing.T, teamID int32, date string) sqlcdb.YahooTeamRoster {
	t.Helper()
	parsed, err := time.Parse(time.DateOnly, date)
	require.NoError(t, err)
	return sqlcdb.YahooTeamRoster{TeamID: teamID, Date: pgtype.Date{Time: parsed, Valid: true}}
}
