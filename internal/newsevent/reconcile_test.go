package newsevent

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	recPlayerNHLID    int64 = 9900101
	recOtherNHLID     int64 = 9900102
	recArticle        int64 = 10
	recOtherArticle   int64 = 20
	recThirdArticle   int64 = 30
	recVersion        int64 = 100
	recEarlierVersion int64 = 99
	recExtraction     int64 = 7
	recEventID        int64 = 1
	recSecondEventID  int64 = 2
	recWindow               = 14 * 24 * time.Hour
	recGames                = 5
	recOtherGames           = 10
	recDays                 = 14
	recOtherDays            = 28
	recPublisher            = "RotoWire"
	recOtherPublisher       = "Sportsnet"
	recOfficial             = "NHL.com"
)

var recNow = time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)

func recPlayer() news.Identity {
	return news.Identity{NHLPlayerID: recPlayerNHLID, Name: "Test Player"}
}

func recEvent(typ Type, status ReportStatus, d Duration) Event {
	if d.Kind == "" {
		d.Kind = DurationUnknown
	}
	return Event{Player: recPlayer(), Type: typ, Status: status, Duration: d,
		Evidence: []Quote{{Doc: firstDocumentRef, Text: "the quoted words here"}}}
}

func recStored(id int64, e Event, first time.Time, support ...Support) StoredEvent {
	return StoredEvent{ID: id, Event: e, Lifecycle: LifecycleActive, FirstReportedAt: first, LastReportedAt: first, Support: support}
}

func recReport(publisher string, kind news.Kind, article int64, at time.Time) Report {
	return Report{VersionID: recVersion, ArticleID: article, Version: 1, Publisher: publisher, Kind: kind,
		ReportedAt: at, ExtractionID: recExtraction, Clean: true}
}

func games(n int) Duration { return Duration{Kind: DurationGames, Games: n} }
func days(n int) Duration  { return Duration{Kind: DurationDays, Days: n} }

func TestReconcileCreatesAnEventForANewReport(t *testing.T) {
	e := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	plan := Reconcile(nil, recReport(recOfficial, news.KindOfficial, recArticle, recNow), []Event{e}, recWindow)

	require.Len(t, plan.Create, 1)
	assert.Equal(t, LifecycleActive, plan.Create[0].Lifecycle)
	assert.Equal(t, []EvidenceLink{{Ref: EventRef{New: 1}, Relation: RelationSupports, Quotes: e.Evidence}}, plan.Evidence)
	assert.Empty(t, plan.Transitions)
}

func TestReconcileRepeatedReportSupportsTheEventOnRecord(t *testing.T) {
	e := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	existing := []StoredEvent{recStored(recEventID, e, recNow.Add(-time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	plan := Reconcile(existing, recReport(recPublisher, news.KindReporting, recArticle, recNow), []Event{e}, recWindow)

	assert.Empty(t, plan.Create, "a syndicated or repeated report adds no event")
	assert.Equal(t, []int64{recEventID}, plan.Touch)
	require.Len(t, plan.Evidence, 1)
	assert.Equal(t, EventRef{ID: recEventID}, plan.Evidence[0].Ref)
	assert.Empty(t, plan.Reviews)
}

func TestReconcileFirmerReportSupersedes(t *testing.T) {
	rumor := recEvent(TypeTrade, StatusRumor, Duration{})
	existing := []StoredEvent{recStored(recEventID, rumor, recNow.Add(-time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOtherPublisher})}
	confirmed := recEvent(TypeTrade, StatusConfirmed, Duration{})
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow), []Event{confirmed}, recWindow)

	require.Len(t, plan.Create, 1)
	assert.Equal(t, LifecycleActive, plan.Create[0].Lifecycle)
	require.Len(t, plan.Transitions, 1)
	assert.Equal(t, LifecycleSuperseded, plan.Transitions[0].To)
	assert.Equal(t, EventRef{New: 1}, plan.Transitions[0].SupersededBy)
}

func TestReconcileDifferingReportsRouteToReview(t *testing.T) {
	first := recEvent(TypeInjury, StatusReported, days(recDays))
	existing := []StoredEvent{recStored(recEventID, first, recNow.Add(-11*time.Hour), Support{ArticleID: recOtherArticle, Publisher: recPublisher})}
	second := recEvent(TypeInjury, StatusReported, days(recOtherDays))
	plan := Reconcile(existing, recReport(recOtherPublisher, news.KindReporting, recArticle, recNow), []Event{second}, recWindow)

	require.Len(t, plan.Create, 1)
	assert.Equal(t, LifecycleActive, plan.Create[0].Lifecycle, "both claims stay on record")
	assert.NotEmpty(t, plan.Create[0].Review)
	assert.Empty(t, plan.Transitions)
	require.Len(t, plan.Reviews, 1)
	assert.Equal(t, recEventID, plan.Reviews[0].EventID)
}

func TestReconcileLaterEqualReportIsAnUpdate(t *testing.T) {
	first := recEvent(TypeInjury, StatusReported, Duration{Kind: DurationDayToDay})
	existing := []StoredEvent{recStored(recEventID, first, recNow.Add(-5*24*time.Hour), Support{ArticleID: recOtherArticle, Publisher: recPublisher})}
	later := recEvent(TypeInjury, StatusReported, Duration{Kind: DurationWeekToWeek})
	plan := Reconcile(existing, recReport(recOtherPublisher, news.KindReporting, recArticle, recNow), []Event{later}, recWindow)

	require.Len(t, plan.Transitions, 1, "days apart, a new length is an update rather than a conflict")
	assert.Equal(t, LifecycleSuperseded, plan.Transitions[0].To)
	assert.Empty(t, plan.Reviews)
}

func TestReconcileWeakerReportConflicts(t *testing.T) {
	official := recEvent(TypeSuspension, StatusConfirmed, Duration{Kind: DurationIndefinite})
	existing := []StoredEvent{recStored(recEventID, official, recNow.Add(-10*24*time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	reported := recEvent(TypeSuspension, StatusReported, games(recGames))
	plan := Reconcile(existing, recReport(recPublisher, news.KindReporting, recArticle, recNow), []Event{reported}, recWindow)

	assert.Empty(t, plan.Transitions, "a reporter cannot replace an official indefinite suspension")
	require.Len(t, plan.Reviews, 1)
}

func TestReconcileLateOlderReportIsRecordedAsSuperseded(t *testing.T) {
	newer := recEvent(TypeInjury, StatusConfirmed, days(recOtherDays))
	existing := []StoredEvent{recStored(recEventID, newer, recNow, Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	older := recEvent(TypeInjury, StatusConfirmed, Duration{Kind: DurationDayToDay})
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow.Add(-2*24*time.Hour)), []Event{older}, recWindow)

	require.Len(t, plan.Create, 1)
	assert.Equal(t, LifecycleSuperseded, plan.Create[0].Lifecycle)
	assert.Equal(t, recEventID, plan.Create[0].SupersededBy)
	assert.Empty(t, plan.Transitions, "an older report never replaces a newer one")
}

func TestReconcileDenials(t *testing.T) {
	confirmed := recEvent(TypeSuspension, StatusConfirmed, games(recGames))
	denial := recEvent(TypeSuspension, StatusDenied, Duration{})
	elsewhere := Support{ArticleID: recOtherArticle, Publisher: recOtherPublisher}

	official := Reconcile([]StoredEvent{recStored(recEventID, confirmed, recNow.Add(-time.Hour), elsewhere)},
		recReport(recOfficial, news.KindOfficial, recArticle, recNow), []Event{denial}, recWindow)
	require.Len(t, official.Transitions, 1)
	assert.Equal(t, LifecycleRetracted, official.Transitions[0].To)
	assert.Equal(t, RelationRetracts, official.Evidence[0].Relation)
	assert.Empty(t, official.Create, "a denial is never stored as an event")

	reporter := Reconcile([]StoredEvent{recStored(recEventID, confirmed, recNow.Add(-time.Hour), elsewhere)},
		recReport(recPublisher, news.KindReporting, recArticle, recNow), []Event{denial}, recWindow)
	assert.Empty(t, reporter.Transitions, "another reporter's denial only contradicts")
	assert.Equal(t, RelationContradicts, reporter.Evidence[0].Relation)
	require.Len(t, reporter.Reviews, 1)

	rumor := recEvent(TypeSuspension, StatusRumor, Duration{})
	denied := Reconcile([]StoredEvent{recStored(recEventID, rumor, recNow.Add(-time.Hour), elsewhere)},
		recReport(recPublisher, news.KindReporting, recArticle, recNow), []Event{denial}, recWindow)
	require.Len(t, denied.Transitions, 1, "a rumor yields to a denial")
	assert.Equal(t, LifecycleRetracted, denied.Transitions[0].To)
}

func TestReconcileReinstatementResolvesSuspensionsOfAnyAge(t *testing.T) {
	old := recEvent(TypeSuspension, StatusConfirmed, Duration{Kind: DurationIndefinite})
	existing := []StoredEvent{recStored(recEventID, old, recNow.Add(-3*recWindow), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	stated := recEvent(TypeInjury, StatusConfirmed, Duration{})
	reinstated := recEvent(TypeReinstatement, StatusConfirmed, Duration{})
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow), []Event{stated, reinstated}, recWindow)

	require.Len(t, plan.Transitions, 1)
	assert.Equal(t, LifecycleResolved, plan.Transitions[0].To)
	require.Len(t, plan.Create, 2)
	assert.Equal(t, LifecycleResolved, plan.Create[0].Lifecycle, "an injury the reinstating report itself states is over")
	assert.Equal(t, LifecycleActive, plan.Create[1].Lifecycle)
}

func TestReconcileRumoredReinstatementResolvesNothing(t *testing.T) {
	old := recEvent(TypeSuspension, StatusConfirmed, Duration{Kind: DurationIndefinite})
	existing := []StoredEvent{recStored(recEventID, old, recNow.Add(-time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	plan := Reconcile(existing, recReport(recPublisher, news.KindReporting, recArticle, recNow),
		[]Event{recEvent(TypeReinstatement, StatusRumor, Duration{})}, recWindow)
	assert.Empty(t, plan.Transitions)
}

func TestReconcileNewInjuryEndsAnEarlierReinstatement(t *testing.T) {
	back := recEvent(TypeReinstatement, StatusConfirmed, Duration{})
	existing := []StoredEvent{recStored(recEventID, back, recNow.Add(-time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow),
		[]Event{recEvent(TypeInjury, StatusConfirmed, Duration{Kind: DurationDayToDay})}, recWindow)

	require.Len(t, plan.Transitions, 1)
	assert.Equal(t, LifecycleSuperseded, plan.Transitions[0].To)
	assert.Equal(t, EventRef{New: 1}, plan.Transitions[0].SupersededBy)
}

func TestReconcileInjuryOnInjuredReserveIsTheSameInjury(t *testing.T) {
	dtd := recEvent(TypeInjury, StatusConfirmed, Duration{Kind: DurationDayToDay})
	existing := []StoredEvent{recStored(recEventID, dtd, recNow.Add(-4*24*time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	ir := recEvent(TypeInjury, StatusConfirmed, days(recOtherDays))
	ir.Change = Change{Field: ChangeRosterStatus, To: "injured reserve"}
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow), []Event{ir}, recWindow)

	require.Len(t, plan.Transitions, 1, "placing an injured player on IR updates the injury")
	assert.Equal(t, LifecycleSuperseded, plan.Transitions[0].To)
}

func TestReconcileRoleChangesAreKeyedByWhatMoves(t *testing.T) {
	assigned := recEvent(TypeRoleChange, StatusConfirmed, Duration{})
	assigned.Change = Change{Field: ChangeLeague, To: "Laval Rocket"}
	existing := []StoredEvent{recStored(recEventID, assigned, recNow.Add(-time.Hour), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	captain := recEvent(TypeRoleChange, StatusConfirmed, Duration{})
	captain.Change = Change{Field: ChangeRole, To: "captain"}
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow), []Event{captain}, recWindow)

	require.Len(t, plan.Create, 1)
	assert.Empty(t, plan.Transitions, "a new role does not replace a league assignment")
}

func TestReconcileIgnoresEventsOutsideTheWindow(t *testing.T) {
	old := recEvent(TypeInjury, StatusConfirmed, Duration{Kind: DurationDayToDay})
	existing := []StoredEvent{recStored(recEventID, old, recNow.Add(-2*recWindow), Support{ArticleID: recOtherArticle, Publisher: recOfficial})}
	plan := Reconcile(existing, recReport(recOfficial, news.KindOfficial, recArticle, recNow),
		[]Event{recEvent(TypeInjury, StatusConfirmed, days(recDays))}, recWindow)

	require.Len(t, plan.Create, 1)
	assert.Empty(t, plan.Transitions)
	assert.Empty(t, plan.Reviews)
}

func TestReconcileInnerConflictGoesToReview(t *testing.T) {
	plan := Reconcile(nil, recReport(recPublisher, news.KindReporting, recArticle, recNow),
		[]Event{recEvent(TypeSuspension, StatusReported, games(recGames)), recEvent(TypeSuspension, StatusReported, games(recOtherGames))},
		recWindow)
	require.Len(t, plan.Create, 2)
	assert.Contains(t, plan.Create[0].Review, reviewInnerConflict)
	assert.Contains(t, plan.Create[1].Review, reviewInnerConflict)
}
