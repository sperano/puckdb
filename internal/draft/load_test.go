package draft

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	loadTestSeason    = 2026
	loadTestLeagueID  = 1001
	loadTestNHLPlayer = int64(8470001)
)

// fakeQueries is a hand-written stand-in for draft.Queries: no database, just
// canned returns per method.
type fakeQueries struct {
	snapshot    sqlcdb.YahooLeagueRuleSnapshot
	snapshotErr error
	count       int64
	countErr    error
	league      sqlcdb.YahooLeague
	leagueErr   error
	team        sqlcdb.YahooTeam
	teamErr     error
	players     []sqlcdb.ListYahooLeaguePlayersWithNHLRow
	playersErr  error
}

func (f *fakeQueries) GetLatestYahooLeagueRuleSnapshot(context.Context, sqlcdb.GetLatestYahooLeagueRuleSnapshotParams) (sqlcdb.YahooLeagueRuleSnapshot, error) {
	return f.snapshot, f.snapshotErr
}

func (f *fakeQueries) CountYahooLeagueRuleSnapshots(context.Context, sqlcdb.CountYahooLeagueRuleSnapshotsParams) (int64, error) {
	return f.count, f.countErr
}

func (f *fakeQueries) GetYahooLeague(context.Context, int32) (sqlcdb.YahooLeague, error) {
	return f.league, f.leagueErr
}

func (f *fakeQueries) GetYahooOwnedTeam(context.Context, int32) (sqlcdb.YahooTeam, error) {
	return f.team, f.teamErr
}

func (f *fakeQueries) ListYahooLeaguePlayersWithNHL(context.Context, string) ([]sqlcdb.ListYahooLeaguePlayersWithNHLRow, error) {
	return f.players, f.playersErr
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TestLoadSnapshot_NoRows(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{snapshotErr: pgx.ErrNoRows}
	_, err := LoadSnapshot(context.Background(), q, loadTestSeason, loadTestLeagueID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoRules))
}

func TestLoadSnapshot_DecodesRulesAndVersions(t *testing.T) {
	t.Parallel()
	rules := Rules{LeagueKey: "465.l.1001", LeagueID: loadTestLeagueID, Season: loadTestSeason, ScoringType: scoringTypeRoto}
	hash, data, err := rules.Hash()
	require.NoError(t, err)
	fetchedAt := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	q := &fakeQueries{
		snapshot: sqlcdb.YahooLeagueRuleSnapshot{
			ID: 1, Rules: data, RulesHash: hash, Source: string(SourceYahooAPI),
			SourceSeason: loadTestSeason, SourceLeagueKey: rules.LeagueKey,
			FetchedAt: timestamptz(fetchedAt), FirstSeenAt: timestamptz(fetchedAt), LastSeenAt: timestamptz(fetchedAt),
		},
		count: 3,
	}
	snapshot, err := LoadSnapshot(context.Background(), q, loadTestSeason, loadTestLeagueID)
	require.NoError(t, err)
	assert.Equal(t, rules, snapshot.Rules)
	assert.Equal(t, hash, snapshot.Hash)
	assert.Equal(t, SourceYahooAPI, snapshot.Source)
	assert.EqualValues(t, 3, snapshot.Versions)
	assert.True(t, snapshot.FetchedAt.Equal(fetchedAt))
}

func TestLoadSnapshot_CountError(t *testing.T) {
	t.Parallel()
	rules := Rules{LeagueKey: "465.l.1001"}
	_, data, err := rules.Hash()
	require.NoError(t, err)
	q := &fakeQueries{
		snapshot: sqlcdb.YahooLeagueRuleSnapshot{Rules: data},
		countErr: errors.New("boom"),
	}
	_, err = LoadSnapshot(context.Background(), q, loadTestSeason, loadTestLeagueID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count rule versions")
}

// validSnapshotQueries returns a fakeQueries whose rules snapshot decodes
// cleanly, for tests that exercise loadOwnedTeam and LoadPool through
// LoadLeagueReport.
func validSnapshotQueries(t *testing.T) *fakeQueries {
	t.Helper()
	rules := Rules{LeagueKey: "465.l.1001", LeagueID: loadTestLeagueID, Season: loadTestSeason, ScoringType: scoringTypeRoto}
	_, data, err := rules.Hash()
	require.NoError(t, err)
	return &fakeQueries{snapshot: sqlcdb.YahooLeagueRuleSnapshot{Rules: data}}
}

func TestLoadLeagueReport_OwnedTeamFound(t *testing.T) {
	t.Parallel()
	q := validSnapshotQueries(t)
	q.league = sqlcdb.YahooLeague{Season: loadTestSeason}
	q.team = sqlcdb.YahooTeam{ID: 9, Name: "My Team", DraftPosition: pgtype.Int4{Int32: 3, Valid: true}}

	report, err := LoadLeagueReport(context.Background(), q, loadTestSeason, loadTestLeagueID, time.Now(), poolTestMaxAge)
	require.NoError(t, err)
	require.NotNil(t, report.OwnedTeam)
	assert.Equal(t, 9, report.OwnedTeam.TeamID)
	assert.Equal(t, "My Team", report.OwnedTeam.Name)
	assert.Equal(t, 3, report.OwnedTeam.DraftPosition)
	assert.Empty(t, report.Notes)
}

func TestLoadLeagueReport_LeagueFromAnotherSeason(t *testing.T) {
	t.Parallel()
	q := validSnapshotQueries(t)
	q.league = sqlcdb.YahooLeague{Season: loadTestSeason + 1, LeagueKey: "465.l.1001"}

	report, err := LoadLeagueReport(context.Background(), q, loadTestSeason, loadTestLeagueID, time.Now(), poolTestMaxAge)
	require.NoError(t, err)
	assert.Nil(t, report.OwnedTeam)
	require.Len(t, report.Notes, 1)
	assert.Contains(t, report.Notes[0], "not this league's")
}

func TestLoadLeagueReport_LeagueMissing(t *testing.T) {
	t.Parallel()
	q := validSnapshotQueries(t)
	q.leagueErr = pgx.ErrNoRows

	report, err := LoadLeagueReport(context.Background(), q, loadTestSeason, loadTestLeagueID, time.Now(), poolTestMaxAge)
	require.NoError(t, err)
	assert.Nil(t, report.OwnedTeam)
	require.Len(t, report.Notes, 1)
	assert.Contains(t, report.Notes[0], "is not in yahoo_leagues")
}

func TestLoadLeagueReport_NoOwnedTeam(t *testing.T) {
	t.Parallel()
	q := validSnapshotQueries(t)
	q.league = sqlcdb.YahooLeague{Season: loadTestSeason}
	q.teamErr = pgx.ErrNoRows

	report, err := LoadLeagueReport(context.Background(), q, loadTestSeason, loadTestLeagueID, time.Now(), poolTestMaxAge)
	require.NoError(t, err)
	assert.Nil(t, report.OwnedTeam)
	require.Len(t, report.Notes, 1)
	assert.Contains(t, report.Notes[0], "your team is unknown")
}

func TestLoadPool_MapsNHLPlayerAndPositions(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{players: []sqlcdb.ListYahooLeaguePlayersWithNHLRow{
		{
			PlayerID: 7001, PlayerKey: "465.p.7001", FullName: "Alex Twoway",
			EligiblePositions: []string{PositionCenter, PositionLeftWing, SlotUtility},
			NhlPlayerID:       pgtype.Int8{Int64: loadTestNHLPlayer, Valid: true},
		},
		{
			PlayerID: 7006, PlayerKey: "465.p.7006", FullName: "Finn Nopos",
			NhlPlayerID: pgtype.Int8{},
		},
	}}
	players, err := LoadPool(context.Background(), q, "465.l.1001")
	require.NoError(t, err)
	require.Len(t, players, 2)
	assert.EqualValues(t, loadTestNHLPlayer, players[0].NHLPlayerID)
	assert.Equal(t, []string{PositionCenter, PositionLeftWing, SlotUtility}, players[0].EligiblePositions)
	assert.Zero(t, players[1].NHLPlayerID)
}
