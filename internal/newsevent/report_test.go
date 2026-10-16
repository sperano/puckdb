package newsevent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// chasePlayer is the player of the digest fixture's superseded pair.
var chasePlayer = news.Identity{NHLPlayerID: 5002, Name: "Owen Chase"}

func reportedEventIDs(events []ReportedEvent) []int64 {
	ids := make([]int64, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	return ids
}

func TestLoadPlayerEvents_KeepsActiveEventsByDefault(t *testing.T) {
	f := newDigestFixture(t)

	got, err := LoadPlayerEvents(context.Background(), f.db, PlayerEventsRequest{Player: chasePlayer})
	require.NoError(t, err)
	assert.Equal(t, 1, got.Matching)
	assert.Equal(t, []int64{f.eventC}, reportedEventIDs(got.Events))
}

func TestLoadPlayerEvents_IncludeInactiveInFirstReportedOrder(t *testing.T) {
	f := newDigestFixture(t)

	got, err := LoadPlayerEvents(context.Background(), f.db, PlayerEventsRequest{Player: chasePlayer, IncludeInactive: true})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Matching)
	assert.Equal(t, []int64{f.eventB, f.eventC}, reportedEventIDs(got.Events))
	assert.Equal(t, LifecycleSuperseded, got.Events[0].Lifecycle)
	assert.Equal(t, f.eventC, got.Events[0].SupersededBy)
	assert.Len(t, got.Events[0].History, 2, "history is loaded with the event")
}

func TestLoadPlayerEvents_LimitKeepsLatestAndCountsMatching(t *testing.T) {
	f := newDigestFixture(t)

	got, err := LoadPlayerEvents(context.Background(), f.db,
		PlayerEventsRequest{Player: chasePlayer, IncludeInactive: true, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Matching, "matching counts the events before the limit")
	assert.Equal(t, []int64{f.eventC}, reportedEventIDs(got.Events))
}

func TestLoadPlayerEvents_SinceFiltersOnLastReport(t *testing.T) {
	f := newDigestFixture(t)
	req := PlayerEventsRequest{Player: chasePlayer, IncludeInactive: true, Since: f.now.Add(-2 * time.Hour)}

	got, err := LoadPlayerEvents(context.Background(), f.db, req)
	require.NoError(t, err)
	assert.Equal(t, []int64{f.eventC}, reportedEventIDs(got.Events))

	req.Since = f.now.Add(-6 * time.Hour)
	got, err = LoadPlayerEvents(context.Background(), f.db, req)
	require.NoError(t, err)
	assert.Equal(t, []int64{f.eventB, f.eventC}, reportedEventIDs(got.Events), "since is inclusive")
}

func TestLoadPlayerEvents_LoadsDecodedEvidence(t *testing.T) {
	f := newDigestFixture(t)

	got, err := LoadPlayerEvents(context.Background(), f.db, PlayerEventsRequest{Player: f.playerA})
	require.NoError(t, err)
	require.Len(t, got.Events, 1)
	ev := got.Events[0]
	assert.True(t, ev.Event.NeedsReview)
	require.Len(t, ev.Evidence, 1)
	assert.Equal(t, "NHL.com", ev.Evidence[0].Publisher)
	assert.Equal(t, []Quote{{Doc: "E1", Text: "placed on injured reserve"}}, ev.Evidence[0].Quotes)
}

// TestLoadPlayerEvents_MatchesEitherID finds an event stored under the
// Yahoo ID only when the request names the player by both IDs.
func TestLoadPlayerEvents_MatchesEitherID(t *testing.T) {
	f := newDigestFixture(t)
	const yahooID = 7001
	yahooOnly, err := f.db.CreateNewsEvent(context.Background(), sqlcdb.CreateNewsEventParams{
		YahooPlayerID: pgInt4(yahooID), PlayerName: "Alex Rivers", EventType: string(TypeTrade),
		ReportStatus: string(StatusRumor), DurationKind: string(DurationUnknown), Lifecycle: string(LifecycleActive),
		FirstReportedAt: news.Timestamptz(f.now),
	})
	require.NoError(t, err)

	byNHL, err := LoadPlayerEvents(context.Background(), f.db, PlayerEventsRequest{Player: f.playerA})
	require.NoError(t, err)
	assert.Equal(t, []int64{f.eventA}, reportedEventIDs(byNHL.Events))

	both := f.playerA
	both.YahooPlayerID = yahooID
	byBoth, err := LoadPlayerEvents(context.Background(), f.db, PlayerEventsRequest{Player: both})
	require.NoError(t, err)
	assert.Equal(t, []int64{f.eventA, yahooOnly}, reportedEventIDs(byBoth.Events))
}

func TestLoadPlayerEvents_RequiresAPlayerID(t *testing.T) {
	_, err := LoadPlayerEvents(context.Background(), newFakeDB(), PlayerEventsRequest{Player: news.Identity{Name: "Nobody"}})
	require.ErrorContains(t, err, "an NHL or Yahoo player ID is required")
}
