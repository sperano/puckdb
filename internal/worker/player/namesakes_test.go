package player

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeNamesakes is a NamesakeSeasonLister returning rows or err, recording
// the parameters of each call.
type fakeNamesakes struct {
	rows  []sqlcdb.ListNamesakeNHLSeasonsRow
	err   error
	calls []sqlcdb.ListNamesakeNHLSeasonsParams
}

func (f *fakeNamesakes) ListNamesakeNHLSeasons(_ context.Context, arg sqlcdb.ListNamesakeNHLSeasonsParams) ([]sqlcdb.ListNamesakeNHLSeasonsRow, error) {
	f.calls = append(f.calls, arg)
	return f.rows, f.err
}

func namesakeRow(playerID int64, startYear int32) sqlcdb.ListNamesakeNHLSeasonsRow {
	return sqlcdb.ListNamesakeNHLSeasonsRow{PlayerID: playerID, StartYear: startYear}
}

func TestGroupNamesakeSeasons(t *testing.T) {
	t.Parallel()
	rows := []sqlcdb.ListNamesakeNHLSeasonsRow{
		namesakeRow(8451789, 1999), namesakeRow(8451789, 2000), namesakeRow(8467510, 2000), namesakeRow(8470000, 2010),
	}

	assert.Equal(t, [][]int{{1999, 2000}, {2000}, {2010}}, groupNamesakeSeasons(rows))
	assert.Nil(t, groupNamesakeSeasons(nil))
}

// The lookup asks for the landing's normalized name without the landing's
// own player, and wraps a query error.
func TestNamesakeSeasons(t *testing.T) {
	t.Parallel()
	landing := &nhl.PlayerLanding{
		PlayerID:  nhl.PlayerID(8467510),
		FirstName: nhl.LocalizedString{Default: " Petr "},
		LastName:  nhl.LocalizedString{Default: "Svobodá"},
	}
	wantParams := sqlcdb.ListNamesakeNHLSeasonsParams{
		FirstNameNormalized: "petr", LastNameNormalized: "svoboda", ExcludePlayerID: 8467510,
	}

	t.Run("seasons", func(t *testing.T) {
		t.Parallel()
		lister := &fakeNamesakes{rows: []sqlcdb.ListNamesakeNHLSeasonsRow{namesakeRow(8451789, 2000)}}
		a := &Activities{Namesakes: lister}

		got, err := a.namesakeSeasons(context.Background(), landing)()

		require.NoError(t, err)
		assert.Equal(t, [][]int{{2000}}, got)
		assert.Equal(t, []sqlcdb.ListNamesakeNHLSeasonsParams{wantParams}, lister.calls)
	})
	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		errDB := errors.New("database down")
		a := &Activities{Namesakes: &fakeNamesakes{err: errDB}}

		_, err := a.namesakeSeasons(context.Background(), landing)()

		require.ErrorIs(t, err, errDB)
	})
	t.Run("no lister", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, (&Activities{}).namesakeSeasons(context.Background(), landing))
	})
}

// The batch matches the 1979 Mike Green, whose Yahoo Sports page is gone
// (a recorded 404), by the seasons the game lists name his Yahoo ID in,
// with the 1985 Mike Green's seasons from the database.
func TestProcessPlayerBatch_MatchesANamesakeBySeasons(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	landing := &nhl.PlayerLanding{
		PlayerID:  nhl.PlayerID(8468436),
		FirstName: nhl.LocalizedString{Default: "Mike"},
		LastName:  nhl.LocalizedString{Default: "Green"},
		BirthDate: "1979-08-23",
		SeasonTotals: []nhl.SeasonTotal{
			{Season: nhl.NewSeason(2003), GameType: nhl.GameTypeRegularSeason, LeagueAbbrev: "NHL", GamesPlayed: 1},
		},
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), resource.PlayerLanding{PlayerID: landing.PlayerID}.Path(), landingJSON))
	const olderID, youngerID store.YahooPlayerID = 2167, 3667
	writeMarker(t, mem, olderID, missingTestNow)

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	entries := map[string]string{}
	for _, p := range []*store.YahooPlayer{
		{YahooID: olderID, FirstName: "Mike", LastName: "Green", Seasons: []int{2001, 2002, 2003}},
		{YahooID: youngerID, FirstName: "Mike", LastName: "Green", Seasons: []int{2005, 2006}},
	} {
		entries[p.YahooID.String()] = string(encodeYahooPlayer(t, p))
	}
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(entries)
	mockRedis.ExpectSRem(YahooIDAvailableKey, int(olderID)).SetVal(1)

	upserter := NewMockPlayerUpserter()
	upserter.On("ClearConflictingYahooID", mock.Anything, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).Return(nil)
	var upserted sqlcdb.UpsertPlayerParams
	upserter.On("UpsertPlayer", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Run(func(args mock.Arguments) { upserted = args.Get(1).(sqlcdb.UpsertPlayerParams) }).
		Return(nil)
	server := &pageServer{pages: map[store.YahooPlayerID][]byte{youngerID: yahooPlayerPageHTML(eliasDefenceBirth)}}
	careerUpserter := &MockPlayerCareerUpserter{}
	careerUpserter.On("UpsertPlayerSeasonTotalBatch", mock.Anything, mock.AnythingOfType("[]sqlcdb.UpsertPlayerSeasonTotalBatchParams")).
		Return(sqlcdb.NewUpsertPlayerSeasonTotalBatchBatchResults(&mockBatchResults{}, 1))
	a := newTestProcessActivities(mem, &MockNHLClient{}, redisClient, nil, upserter, careerUpserter)
	a.YahooPlayerPages = server.download
	a.Now = fixedNow
	a.Namesakes = &fakeNamesakes{rows: []sqlcdb.ListNamesakeNHLSeasonsRow{namesakeRow(8471242, 2005), namesakeRow(8471242, 2006)}}

	result, err := runProcessPlayerBatch(t, context.Background(), a, []store.BoxscorePlayer{{ID: landing.PlayerID.Int64()}})

	require.NoError(t, err)
	assert.Equal(t, map[string]int{matching.MatchReasonNameSeasons.String(): 1}, result.MatchReasons)
	assert.Equal(t, 1, result.BirthDatePagesMissing)
	assert.Equal(t, 1, result.BirthDatePagesDownloaded, "the younger Green's page is still read for his birth date")
	assert.Equal(t, int64(olderID), upserted.YahooID.Int64)
	require.NoError(t, mockRedis.ExpectationsWereMet(), "the matched ID leaves the available pool")
}
