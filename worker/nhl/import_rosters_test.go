package nhl

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalizedStringToText(t *testing.T) {
	t.Parallel()

	t.Run("nil pointer", func(t *testing.T) {
		result := localizedStringToText(nil)
		assert.False(t, result.Valid)
	})

	t.Run("empty string", func(t *testing.T) {
		ls := &nhlapi.LocalizedString{Default: ""}
		result := localizedStringToText(ls)
		assert.True(t, result.Valid)
		assert.Equal(t, "", result.String)
	})

	t.Run("non-empty string", func(t *testing.T) {
		ls := &nhlapi.LocalizedString{Default: "Montréal"}
		result := localizedStringToText(ls)
		assert.True(t, result.Valid)
		assert.Equal(t, "Montréal", result.String)
	})
}

// mockBatchResults implements pgx.BatchResults for testing.
type mockBatchResults struct {
	execErr error
	count   int
	current int
}

func (m *mockBatchResults) Exec() (pgconn.CommandTag, error) {
	m.current++
	return pgconn.NewCommandTag("INSERT 0 1"), m.execErr
}

func (m *mockBatchResults) Query() (pgx.Rows, error) { return nil, nil }
func (m *mockBatchResults) QueryRow() pgx.Row        { return nil }
func (m *mockBatchResults) Close() error              { return nil }

// fakeRosterUpserter implements SeasonRosterUpserter for testing.
type fakeRosterUpserter struct {
	teams         []sqlcdb.GetSeasonTeamAbbrevsRow
	teamsErr      error
	ensuredParams []sqlcdb.EnsurePlayerExistsBatchParams
	ensureErr     error
	rosterParams  []sqlcdb.UpsertSeasonRosterBatchParams
	rosterErr     error
}

func (f *fakeRosterUpserter) GetSeasonTeamAbbrevs(_ context.Context, _ int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error) {
	return f.teams, f.teamsErr
}

func (f *fakeRosterUpserter) EnsurePlayerExistsBatch(_ context.Context, arg []sqlcdb.EnsurePlayerExistsBatchParams) *sqlcdb.EnsurePlayerExistsBatchBatchResults {
	f.ensuredParams = append(f.ensuredParams, arg...)
	return sqlcdb.NewEnsurePlayerExistsBatchBatchResults(&mockBatchResults{execErr: f.ensureErr, count: len(arg)}, len(arg))
}

func (f *fakeRosterUpserter) UpsertSeasonRosterBatch(_ context.Context, arg []sqlcdb.UpsertSeasonRosterBatchParams) *sqlcdb.UpsertSeasonRosterBatchBatchResults {
	f.rosterParams = append(f.rosterParams, arg...)
	return sqlcdb.NewUpsertSeasonRosterBatchBatchResults(&mockBatchResults{execErr: f.rosterErr, count: len(arg)}, len(arg))
}

func testRosterPlayers() []nhlapi.RosterPlayer {
	return []nhlapi.RosterPlayer{
		{
			ID:             nhlapi.PlayerID(8449552),
			FirstName:      nhlapi.LocalizedString{Default: "Gord"},
			LastName:       nhlapi.LocalizedString{Default: "Wilson"},
			Position:       "D",
			ShootsCatches:  "L",
			Headshot:       "https://example.com/headshot.jpg",
			HeightInInches: 72,
			WeightInPounds: 175,
			BirthDate:      "1932-08-13",
			BirthCountry:   "CAN",
			BirthCity:      &nhlapi.LocalizedString{Default: "Port Arthur"},
			BirthStateProvince: &nhlapi.LocalizedString{Default: "ON"},
			SweaterNumber:  4,
		},
		{
			ID:             nhlapi.PlayerID(8449553),
			FirstName:      nhlapi.LocalizedString{Default: "Hub"},
			LastName:       nhlapi.LocalizedString{Default: "Wilson"},
			Position:       "LW",
			ShootsCatches:  "L",
			HeightInInches: 68,
			WeightInPounds: 160,
			BirthDate:      "1898-10-25",
			BirthCountry:   "CAN",
		},
	}
}

func TestEnsureRosterPlayersExist(t *testing.T) {
	t.Parallel()

	t.Run("creates stub params with correct fields", func(t *testing.T) {
		fake := &fakeRosterUpserter{}
		players := testRosterPlayers()

		err := ensureRosterPlayersExist(context.Background(), fake, players, "BOS")
		require.NoError(t, err)

		require.Len(t, fake.ensuredParams, 2)

		p := fake.ensuredParams[0]
		assert.Equal(t, int64(8449552), p.ID)
		assert.Equal(t, "Gord", p.FirstName)
		assert.Equal(t, "Wilson", p.LastName)
		assert.Equal(t, sqlcdb.NullPlayerPosition{PlayerPosition: "D", Valid: true}, p.Position)
		assert.Equal(t, sqlcdb.NullHandSide{HandSide: "L", Valid: true}, p.ShootsCatches)
		assert.Equal(t, "https://example.com/headshot.jpg", p.HeadshotURL)
		assert.Equal(t, int32(72), p.HeightInches.Int32)
		assert.True(t, p.HeightInches.Valid)
		assert.Equal(t, int32(175), p.WeightPounds.Int32)
		assert.True(t, p.WeightPounds.Valid)
		assert.True(t, p.BirthDate.Valid)
		assert.Equal(t, "Port Arthur", p.BirthCity.String)
		assert.True(t, p.BirthCity.Valid)
		assert.Equal(t, "ON", p.BirthStateProvince.String)
		assert.True(t, p.BirthStateProvince.Valid)
		assert.Equal(t, "CAN", p.BirthCountry.String)
		assert.Equal(t, int32(4), p.SweaterNumber.Int32)
		assert.True(t, p.SweaterNumber.Valid)
	})

	t.Run("handles missing optional fields", func(t *testing.T) {
		fake := &fakeRosterUpserter{}
		players := testRosterPlayers()

		err := ensureRosterPlayersExist(context.Background(), fake, players, "BOS")
		require.NoError(t, err)

		p := fake.ensuredParams[1]
		assert.Equal(t, int64(8449553), p.ID)
		assert.Equal(t, "Hub", p.FirstName)
		assert.False(t, p.BirthCity.Valid)
		assert.False(t, p.BirthStateProvince.Valid)
		assert.Equal(t, "", p.HeadshotURL)
	})

	t.Run("normalizes names", func(t *testing.T) {
		fake := &fakeRosterUpserter{}
		players := []nhlapi.RosterPlayer{{
			ID:        nhlapi.PlayerID(1),
			FirstName: nhlapi.LocalizedString{Default: "  José  "},
			LastName:  nhlapi.LocalizedString{Default: "  García  "},
			Position:  "C",
		}}

		err := ensureRosterPlayersExist(context.Background(), fake, players, "MTL")
		require.NoError(t, err)

		p := fake.ensuredParams[0]
		assert.Equal(t, "José", p.FirstName)
		assert.Equal(t, "García", p.LastName)
		assert.NotEqual(t, p.FirstName, p.FirstNameNormalized)
	})

	t.Run("propagates batch error", func(t *testing.T) {
		fake := &fakeRosterUpserter{ensureErr: errors.New("db connection lost")}
		players := testRosterPlayers()

		err := ensureRosterPlayersExist(context.Background(), fake, players, "BOS")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ensure player 8449552")
		assert.Contains(t, err.Error(), "BOS")
		assert.Contains(t, err.Error(), "db connection lost")
	})

	t.Run("zero height and weight are invalid", func(t *testing.T) {
		fake := &fakeRosterUpserter{}
		players := []nhlapi.RosterPlayer{{
			ID:             nhlapi.PlayerID(1),
			FirstName:      nhlapi.LocalizedString{Default: "Test"},
			LastName:       nhlapi.LocalizedString{Default: "Player"},
			Position:       "C",
			HeightInInches: 0,
			WeightInPounds: 0,
			SweaterNumber:  0,
		}}

		err := ensureRosterPlayersExist(context.Background(), fake, players, "BOS")
		require.NoError(t, err)

		p := fake.ensuredParams[0]
		assert.False(t, p.HeightInches.Valid)
		assert.False(t, p.WeightPounds.Valid)
		assert.False(t, p.SweaterNumber.Valid)
	})

	t.Run("invalid birth date is skipped", func(t *testing.T) {
		fake := &fakeRosterUpserter{}
		players := []nhlapi.RosterPlayer{{
			ID:        nhlapi.PlayerID(1),
			FirstName: nhlapi.LocalizedString{Default: "Test"},
			LastName:  nhlapi.LocalizedString{Default: "Player"},
			Position:  "C",
			BirthDate: "not-a-date",
		}}

		err := ensureRosterPlayersExist(context.Background(), fake, players, "BOS")
		require.NoError(t, err)
		assert.False(t, fake.ensuredParams[0].BirthDate.Valid)
	})
}
