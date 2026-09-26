package newsevent

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// correctionReport is version 2 of recArticle.
func correctionReport(clean bool) Report {
	r := recReport(recPublisher, news.KindReporting, recArticle, recNow)
	r.Version, r.Clean = 2, clean
	return r
}

func TestReconcileCorrectionRetractsWhatOnlyTheArticleSaid(t *testing.T) {
	e := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	existing := []StoredEvent{recStored(recEventID, e, recNow, Support{ArticleID: recArticle, VersionID: recEarlierVersion, Publisher: recPublisher})}
	plan := Reconcile(existing, correctionReport(true), nil, recWindow)

	require.Len(t, plan.Transitions, 1, "the newer version no longer reports it")
	assert.Equal(t, LifecycleRetracted, plan.Transitions[0].To)
	assert.Equal(t, RelationWithdraws, plan.Evidence[0].Relation)
}

func TestReconcileCorrectionKeepsWhatOthersStillReport(t *testing.T) {
	e := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	existing := []StoredEvent{recStored(recEventID, e, recNow,
		Support{ArticleID: recArticle, VersionID: recEarlierVersion, Publisher: recPublisher},
		Support{ArticleID: recThirdArticle, Publisher: recOfficial})}
	plan := Reconcile(existing, correctionReport(true), nil, recWindow)

	assert.Empty(t, plan.Transitions)
	require.Len(t, plan.Evidence, 1)
	assert.Equal(t, RelationWithdraws, plan.Evidence[0].Relation, "the withdrawal stays in the audit trail")
}

func TestReconcileUncleanCorrectionOnlyRoutesToReview(t *testing.T) {
	e := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	existing := []StoredEvent{recStored(recEventID, e, recNow, Support{ArticleID: recArticle, VersionID: recEarlierVersion, Publisher: recPublisher})}
	plan := Reconcile(existing, correctionReport(false), nil, recWindow)

	assert.Empty(t, plan.Transitions, "a reading with dropped claims must not retract anything")
	require.Len(t, plan.Reviews, 1)
}

func TestReconcileNewExtractorDisagreeingOnTheSameVersionOnlyRoutesToReview(t *testing.T) {
	e := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	existing := []StoredEvent{recStored(recEventID, e, recNow, Support{ArticleID: recArticle, VersionID: recVersion, Publisher: recPublisher})}
	plan := Reconcile(existing, recReport(recPublisher, news.KindReporting, recArticle, recNow), nil, recWindow)

	assert.Empty(t, plan.Transitions)
	require.Len(t, plan.Reviews, 1)
}

func TestReconcileRevisionWithNewDetailsSupersedes(t *testing.T) {
	e := recEvent(TypeSuspension, StatusReported, Duration{Kind: DurationIndefinite})
	existing := []StoredEvent{recStored(recEventID, e, recNow, Support{ArticleID: recArticle, VersionID: recEarlierVersion, Publisher: recPublisher})}
	updated := recEvent(TypeSuspension, StatusReported, games(recGames))
	plan := Reconcile(existing, correctionReport(true), []Event{updated}, recWindow)

	require.Len(t, plan.Create, 1)
	require.Len(t, plan.Transitions, 1)
	assert.Equal(t, LifecycleSuperseded, plan.Transitions[0].To)
	assert.Empty(t, plan.Reviews)
}

func TestMemoryStoreAppliesPlans(t *testing.T) {
	store := &MemoryStore{}
	e := recEvent(TypeInjury, StatusReported, days(recDays))
	first := recReport(recPublisher, news.KindReporting, recArticle, recNow)
	out := store.Apply(Reconcile(nil, first, []Event{e}, recWindow), first)
	assert.Equal(t, Outcome{Created: 1}, out)

	second := recReport(recOtherPublisher, news.KindReporting, recOtherArticle, recNow.Add(time.Hour))
	second.VersionID = recVersion + 1
	conflicting := recEvent(TypeInjury, StatusReported, days(recOtherDays))
	near := store.Near([]news.Identity{recPlayer()}, second, recWindow)
	require.Len(t, near, 1)
	out = store.Apply(Reconcile(near, second, []Event{conflicting}, recWindow), second)
	assert.Equal(t, 1, out.Created)
	assert.Equal(t, 2, out.Reviews)
	assert.Len(t, store.Events, 2)
	assert.NotEmpty(t, store.Reviews[recEventID])
	assert.NotEmpty(t, store.Reviews[recSecondEventID])

	again := store.Apply(Reconcile(store.Near([]news.Identity{recPlayer()}, second, recWindow), second, []Event{conflicting}, recWindow), second)
	assert.Equal(t, 1, again.Supported, "reconciling the same report again only re-attaches it")
	assert.Zero(t, again.Created)
	assert.Zero(t, again.Reviews, "review reasons are recorded once")
}

func TestReconcileNewExtractorReadingTheSameVersionDifferentlyGoesToReview(t *testing.T) {
	e := recEvent(TypeSuspension, StatusReported, games(recGames))
	existing := []StoredEvent{recStored(recEventID, e, recNow, Support{ArticleID: recArticle, VersionID: recVersion, Publisher: recPublisher})}
	reread := recEvent(TypeSuspension, StatusReported, Duration{Kind: DurationIndefinite})
	plan := Reconcile(existing, recReport(recPublisher, news.KindReporting, recArticle, recNow), []Event{reread}, recWindow)

	assert.Empty(t, plan.Transitions, "a model disagreeing with another about the same text is not news")
	require.Len(t, plan.Reviews, 1)
	require.Len(t, plan.Create, 1)
	assert.NotEmpty(t, plan.Create[0].Review)
}
