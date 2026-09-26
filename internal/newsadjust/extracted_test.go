package newsadjust

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stored extraction events for the conversion tests. The Hellebuyck-style
// goalie is a fixture for an indefinite team suspension, not a forecast.
const (
	testExtractor          = "anthropic/model/prompt-v1/schema-v1"
	testGoalieNHLID  int64 = 8476945
	testSkaterNHLID  int64 = 8478402
	testYahooID            = 7002
	testPoolKey            = "465.p.7002"
	testJetsID       int64 = 52
	testSuspensionID int64 = 1
	testReinstateID  int64 = 2
)

var (
	testReinstatedAt = time.Date(2026, time.September, 20, 15, 0, 0, 0, time.UTC)
	testRecordDelay  = time.Hour
)

func projectedWithID(p projection.PlayerProjection, id int64) projection.PlayerProjection {
	p.PlayerID = &id
	return p
}

func testExtraction(players ...projection.PlayerProjection) Extraction {
	return Extraction{
		Players:  players,
		Teams:    NewTeamDirectory([]Team{{ID: testJetsID, Abbrev: "WPG", FullName: "Winnipeg Jets", CommonName: "Jets"}}),
		Released: map[string]bool{testExtractor: true},
	}
}

// storedEvent is an event created by one report: its supporting evidence
// and creation transition share the report's extraction and version.
func storedEvent(id int64, player news.Identity, t newsevent.Type, reportVersion int64, reportedAt time.Time) ExtractedEvent {
	return ExtractedEvent{
		ID: id, ExtractorKey: testExtractor,
		Event: newsevent.Event{
			Player: player, Type: t, Status: newsevent.StatusConfirmed,
			Duration: newsevent.Duration{Kind: newsevent.DurationUnknown},
		},
		Evidence:    []ExtractedEvidence{support(reportVersion, reportedAt)},
		Transitions: []ExtractedTransition{transition(newsevent.LifecycleActive, reportVersion, reportedAt)},
	}
}

func support(reportVersion int64, reportedAt time.Time) ExtractedEvidence {
	return evidenceRow(newsevent.RelationSupports, reportVersion, reportedAt)
}

func evidenceRow(relation newsevent.Relation, reportVersion int64, reportedAt time.Time) ExtractedEvidence {
	return ExtractedEvidence{
		VersionID: reportVersion, ExtractionID: reportVersion, Relation: relation, Publisher: "NHL.com",
		Kind: news.KindOfficial, URL: "https://www.nhl.com/news/story", ReportedAt: reportedAt,
		RetrievedAt: reportedAt.Add(time.Minute), AddedAt: reportedAt.Add(testRecordDelay),
		Quotes: []newsevent.Quote{{Doc: "D1", Text: "has been suspended by the team"}},
	}
}

func transition(to newsevent.Lifecycle, reportVersion int64, reportedAt time.Time) ExtractedTransition {
	return ExtractedTransition{To: to, VersionID: reportVersion, ExtractionID: reportVersion, At: reportedAt.Add(testRecordDelay)}
}

func goalieIdentity() news.Identity {
	return news.Identity{NHLPlayerID: testGoalieNHLID, Name: "Goalie Fixture"}
}

// suspensionAndReinstatement is an indefinite team suspension a later
// reinstatement resolves, as extraction stores them.
func suspensionAndReinstatement(reinstatedAt time.Time) []ExtractedEvent {
	suspension := storedEvent(testSuspensionID, goalieIdentity(), newsevent.TypeSuspension, testEvidenceID, testReportedAt)
	suspension.Event.Duration.Kind = newsevent.DurationIndefinite
	suspension.Event.EffectiveOn = testReportedAt.Truncate(hoursPerDay * time.Hour)
	suspension.Evidence = append(suspension.Evidence, evidenceRow(newsevent.RelationResolves, testEvidenceID+1, reinstatedAt))
	suspension.Transitions = append(suspension.Transitions, transition(newsevent.LifecycleResolved, testEvidenceID+1, reinstatedAt))
	reinstatement := storedEvent(testReinstateID, goalieIdentity(), newsevent.TypeReinstatement, testEvidenceID+1, reinstatedAt)
	return []ExtractedEvent{suspension, reinstatement}
}

func convert(t *testing.T, events []ExtractedEvent, x Extraction) []Event {
	t.Helper()
	converted, warnings := ConvertExtracted(events, x)
	require.Empty(t, warnings)
	return converted
}

func versionOf(t *testing.T, events []Event, id string, version int) Event {
	t.Helper()
	for _, e := range events {
		if e.ID == id && e.Version == version {
			return e
		}
	}
	t.Fatalf("no version %d of %s", version, id)
	return Event{}
}

func TestConvertExtracted_SuspensionVersionsFollowTheReinstatement(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	events := convert(t, suspensionAndReinstatement(testReinstatedAt), testExtraction(goalie))
	require.Len(t, events, 3)

	first := versionOf(t, events, "news-event:1", 1)
	assert.Equal(t, testGoalieKey, first.PlayerKey)
	assert.Equal(t, EventSuspension, first.Type)
	assert.Equal(t, LifecycleActive, first.Lifecycle)
	assert.Equal(t, Duration{Kind: DurationIndefinite}, first.Duration)
	assert.Equal(t, testReportedAt.Add(testRecordDelay), first.RecordedAt)
	assert.Equal(t, "has been suspended by the team", first.Evidence[0].Quote)

	resolved := versionOf(t, events, "news-event:1", 2)
	assert.Equal(t, LifecycleResolved, resolved.Lifecycle)
	assert.Equal(t, testReinstatedAt, resolved.EffectiveUntil)

	ret := versionOf(t, events, "news-event:2", 1)
	assert.Equal(t, EventReturn, ret.Type)
	assert.Equal(t, []string{"news-event:1"}, ret.Supersedes)
}

func TestConvertExtracted_ReplayShowsTheSuspensionUntilTheReinstatementWasRecorded(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	events := convert(t, suspensionAndReinstatement(testReinstatedAt), testExtraction(goalie))

	before := testRequest(testBaseline(goalie), events...)
	before.AsOf = testReinstatedAt
	suspended := mustApply(t, before)
	conservative, base, optimistic := missed(suspended, testGoalieKey, ScenarioConservative),
		missed(suspended, testGoalieKey, ScenarioBase), missed(suspended, testGoalieKey, ScenarioOptimistic)
	assert.Greater(t, conservative, base)
	assert.Greater(t, base, optimistic)
	assert.Contains(t, adjustmentFor(t, suspended, testGoalieKey).Assumptions[0], "indefinite absence")

	after := mustApply(t, testRequest(testBaseline(goalie), events...))
	for _, s := range Scenarios {
		assert.Zero(t, missed(after, testGoalieKey, s), s)
	}
	assert.Equal(t, OutcomeApplied, decisionFor(t, after, "news-event:2").Outcome)
}

func TestConvertExtracted_ReportWithNothingNewAddsNoVersion(t *testing.T) {
	later := testReportedAt.Add(2 * hoursPerDay * time.Hour)
	e := storedEvent(testSuspensionID, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt)
	e.Evidence = append(e.Evidence, evidenceRow(newsevent.RelationContradicts, testEvidenceID+1, later))
	events := convert(t, []ExtractedEvent{e}, testExtraction())
	require.Len(t, events, 1)

	second := support(testEvidenceID+2, later)
	second.Publisher, second.Kind = "TSN", news.KindReporting
	e.Evidence = append(e.Evidence, second)
	events = convert(t, []ExtractedEvent{e}, testExtraction())
	require.Len(t, events, 2)
	assert.Len(t, versionOf(t, events, "news-event:1", 2).Evidence, 2)
	assert.Equal(t, second.AddedAt, versionOf(t, events, "news-event:1", 2).RecordedAt)
}

func TestConvertExtracted_RetractionEndsTheEffectFromWhenItWasRecorded(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	retractedAt := testReportedAt.Add(3 * hoursPerDay * time.Hour)
	e := storedEvent(testSuspensionID, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt)
	e.Transitions = append(e.Transitions, transition(newsevent.LifecycleRetracted, testEvidenceID+1, retractedAt))
	events := convert(t, []ExtractedEvent{e}, testExtraction(goalie))

	before := testRequest(testBaseline(goalie), events...)
	before.AsOf = retractedAt
	assert.Positive(t, missed(mustApply(t, before), testGoalieKey, ScenarioBase))
	after := mustApply(t, testRequest(testBaseline(goalie), events...))
	assert.Zero(t, missed(after, testGoalieKey, ScenarioBase))
	assert.Equal(t, "retracted by its source", decisionFor(t, after, "news-event:1").Reason)
}

func TestConvertExtracted_ReviewAndUnreleasedExtractorOnlyAlert(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	review := storedEvent(testSuspensionID, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt)
	review.Event.NeedsReview, review.Event.ReviewReason = true, "contradicted by TSN"
	unreleased := storedEvent(testReinstateID, goalieIdentity(), newsevent.TypeSuspension, testEvidenceID+1, testReportedAt)
	unreleased.ExtractorKey = "other/extractor"
	events := convert(t, []ExtractedEvent{review, unreleased}, testExtraction(goalie))
	assert.Contains(t, events[0].Hold, "routed to review: contradicted by TSN")
	assert.Contains(t, events[1].Hold, `extractor "other/extractor" has not passed its evaluation`)

	result := mustApply(t, testRequest(testBaseline(goalie), events...))
	assert.Equal(t, OutcomeAlert, decisionFor(t, result, "news-event:1").Outcome)
	assert.Equal(t, OutcomeAlert, decisionFor(t, result, "news-event:2").Outcome)
	assert.Zero(t, missed(result, testGoalieKey, ScenarioConservative))
	assert.Len(t, adjustmentFor(t, result, testGoalieKey).Alerts, 2)
}

func TestConvertExtracted_HeldReinstatementDoesNotLiftThePenalty(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	events := suspensionAndReinstatement(testReinstatedAt)
	events[1].Event.NeedsReview, events[1].Event.ReviewReason = true, "denied by the team"
	converted := convert(t, events, testExtraction(goalie))
	for _, e := range converted {
		if e.ID == "news-event:1" {
			assert.Equal(t, LifecycleActive, e.Lifecycle)
		}
	}
	result := mustApply(t, testRequest(testBaseline(goalie), converted...))
	assert.Positive(t, missed(result, testGoalieKey, ScenarioBase))
}

func TestConvertExtracted_StatusesAndStatedLengths(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	days := storedEvent(1, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt)
	days.Event.Status = newsevent.StatusReported
	days.Event.Duration = newsevent.Duration{Kind: newsevent.DurationDays, Days: 10}
	until := storedEvent(2, goalieIdentity(), newsevent.TypeSuspension, testEvidenceID+1, testReportedAt)
	until.Event.Duration = newsevent.Duration{Kind: newsevent.DurationUntilDate, Until: testSeasonStart.Add(-time.Hour)}
	rumor := storedEvent(3, goalieIdentity(), newsevent.TypeInjury, testEvidenceID+2, testReportedAt)
	rumor.Event.Status = newsevent.StatusRumor
	backwards := storedEvent(4, goalieIdentity(), newsevent.TypeInjury, testEvidenceID+3, testReportedAt)
	backwards.Event.Duration = newsevent.Duration{Kind: newsevent.DurationUntilDate, Until: testReportedAt.Add(-time.Hour)}
	events := convert(t, []ExtractedEvent{days, until, rumor, backwards}, testExtraction(goalie))

	assert.Equal(t, StatusReported, events[0].Status)
	assert.Equal(t, DurationUntil, events[0].Duration.Kind)
	assert.Equal(t, testReportedAt.Add(10*hoursPerDay*time.Hour), events[0].EffectiveUntil)
	assert.Equal(t, testSeasonStart.Add(-time.Hour), events[1].EffectiveUntil)
	assert.Equal(t, StatusRumor, events[2].Status)
	assert.Equal(t, "the stated end does not follow the start", events[3].Hold)

	result := mustApply(t, testRequest(testBaseline(goalie), events...))
	assert.Equal(t, OutcomeApplied, decisionFor(t, result, "news-event:1").Outcome)
	assert.Equal(t, OutcomeAlert, decisionFor(t, result, "news-event:3").Outcome)
	assert.Zero(t, missed(result, testGoalieKey, ScenarioBase), "both stated lengths end before the season")
}

func TestConvertExtracted_TradesAndRoles(t *testing.T) {
	skater := news.Identity{NHLPlayerID: testSkaterNHLID, Name: "Skater Fixture"}
	change := func(id int64, t newsevent.Type, field newsevent.ChangeField, to string) ExtractedEvent {
		e := storedEvent(id, skater, t, testEvidenceID+id, testReportedAt)
		e.Event.Change = newsevent.Change{Field: field, To: to}
		return e
	}
	events := convert(t, []ExtractedEvent{
		change(1, newsevent.TypeTrade, newsevent.ChangeTeam, "the Winnipeg Jets"),
		change(2, newsevent.TypeTrade, newsevent.ChangeTeam, "Seattle"),
		change(3, newsevent.TypeRoleChange, newsevent.ChangeRole, "top power-play unit"),
		change(4, newsevent.TypeRoleChange, newsevent.ChangeRole, "captain"),
		change(5, newsevent.TypeRoleChange, newsevent.ChangeRosterStatus, "waivers"),
	}, testExtraction())

	assert.Equal(t, EventTrade, events[0].Type)
	assert.Equal(t, testJetsID, *events[0].Role.TeamID)
	assert.Empty(t, events[0].Hold)
	assert.Equal(t, `trade destination "Seattle" matches no single NHL team`, events[1].Hold)
	assert.Equal(t, &RoleChange{PowerPlay: DirectionUp}, events[2].Role)
	assert.Equal(t, `role "captain" has no numeric mapping`, events[3].Hold)
	assert.Contains(t, events[4].Hold, `roster status change to "waivers" has no numeric mapping`)
}

func TestConvertExtracted_RecallEndsTheAssignment(t *testing.T) {
	skater := news.Identity{NHLPlayerID: testSkaterNHLID, Name: "Skater Fixture"}
	player := projectedWithID(testSkater(testSkaterKey), testSkaterNHLID)
	assigned := storedEvent(1, skater, newsevent.TypeRoleChange, testEvidenceID, testReportedAt)
	assigned.Event.Change = newsevent.Change{Field: newsevent.ChangeLeague, From: "NHL", To: "AHL Manitoba Moose"}
	recalledAt := testSeasonStart.Add(10 * hoursPerDay * time.Hour)
	recalled := storedEvent(2, skater, newsevent.TypeRoleChange, testEvidenceID+1, recalledAt)
	recalled.Event.Change = newsevent.Change{Field: newsevent.ChangeLeague, From: "AHL", To: "NHL"}
	lonely := storedEvent(3, news.Identity{NHLPlayerID: 1}, newsevent.TypeRoleChange, testEvidenceID+2, recalledAt)
	lonely.Event.Change = recalled.Event.Change
	events := convert(t, []ExtractedEvent{assigned, recalled, lonely}, testExtraction(player))

	assert.Equal(t, EventAbsence, events[0].Type)
	assert.Equal(t, EventReturn, events[1].Type)
	assert.Equal(t, []string{"news-event:1"}, events[1].Supersedes)
	assert.Equal(t, "the recall ends no assignment on record", events[2].Hold)

	result := mustApply(t, Request{
		Baseline: testBaseline(player), Events: events, Policy: DefaultPolicy(), Season: testSeason(),
		AsOf: recalledAt.Add(hoursPerDay * time.Hour),
	})
	assert.InDelta(t, testSeason().position(recalledAt), missed(result, testSkaterKey, ScenarioConservative), testFloatDelta)
}

func TestConvertExtracted_PlayerKeys(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	pool := testSkater(testPoolKey)
	events := convert(t, []ExtractedEvent{
		storedEvent(1, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt),
		storedEvent(2, news.Identity{YahooPlayerID: testYahooID}, newsevent.TypeInjury, testEvidenceID+1, testReportedAt),
		storedEvent(3, news.Identity{NHLPlayerID: 99}, newsevent.TypeInjury, testEvidenceID+2, testReportedAt),
		storedEvent(4, news.Identity{YahooPlayerID: 98}, newsevent.TypeInjury, testEvidenceID+3, testReportedAt),
	}, testExtraction(goalie, pool))
	assert.Equal(t, testGoalieKey, events[0].PlayerKey)
	assert.Equal(t, testPoolKey, events[1].PlayerKey)
	assert.Equal(t, "nhl:99", events[2].PlayerKey)
	assert.Equal(t, "yahoo:98", events[3].PlayerKey)
}

func TestConvertExtracted_EventWithoutSupportIsReported(t *testing.T) {
	e := storedEvent(1, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt)
	e.Evidence = nil
	events, warnings := ConvertExtracted([]ExtractedEvent{e}, testExtraction())
	assert.Empty(t, events)
	assert.Equal(t, []string{"news event 1 has no supporting evidence on record"}, warnings)
}

func TestConvertExtracted_ReinstatementBeforeTheStartRemovesThePenalty(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	events := suspensionAndReinstatement(testReinstatedAt)
	events[0].Event.EffectiveOn = testSeasonStart.Add(3 * hoursPerDay * time.Hour)
	converted := convert(t, events, testExtraction(goalie))
	resolved := versionOf(t, converted, "news-event:1", 2)
	assert.Equal(t, LifecycleResolved, resolved.Lifecycle)
	assert.True(t, resolved.EffectiveUntil.Before(resolved.EffectiveFrom))

	result := mustApply(t, testRequest(testBaseline(goalie), converted...))
	for _, s := range Scenarios {
		assert.Zero(t, missed(result, testGoalieKey, s), s)
	}
}

func TestConvertExtracted_ReportStatingTheInjuryAndItsEndLeavesNoPenalty(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	injury := storedEvent(testSuspensionID, goalieIdentity(), newsevent.TypeInjury, testEvidenceID, testReportedAt)
	injury.Transitions = []ExtractedTransition{transition(newsevent.LifecycleResolved, testEvidenceID, testReportedAt)}
	reinstatement := storedEvent(testReinstateID, goalieIdentity(), newsevent.TypeReinstatement, testEvidenceID, testReportedAt)
	events := convert(t, []ExtractedEvent{injury, reinstatement}, testExtraction(goalie))

	resolved := versionOf(t, events, "news-event:1", 1)
	assert.Equal(t, LifecycleResolved, resolved.Lifecycle)
	assert.Equal(t, testReportedAt, resolved.EffectiveUntil)
	ret := versionOf(t, events, "news-event:2", 1)
	assert.Empty(t, ret.Hold)
	assert.Equal(t, []string{"news-event:1"}, ret.Supersedes)
	result := mustApply(t, testRequest(testBaseline(goalie), events...))
	for _, s := range Scenarios {
		assert.Zero(t, missed(result, testGoalieKey, s), s)
	}
}

func TestConvertExtracted_ReinstatementsOfTwoPlayersInOneReportStayApart(t *testing.T) {
	goalie := projectedWithID(testGoalie(testGoalieKey, testGoalieStarts), testGoalieNHLID)
	skater := projectedWithID(testSkater(testSkaterKey), testSkaterNHLID)
	events := suspensionAndReinstatement(testReinstatedAt)
	other := storedEvent(3, news.Identity{NHLPlayerID: testSkaterNHLID}, newsevent.TypeReinstatement, testEvidenceID+1, testReinstatedAt)
	other.Event.NeedsReview, other.Event.ReviewReason = true, "contradicted by TSN"
	converted := convert(t, append(events, other), testExtraction(goalie, skater))
	assert.Equal(t, LifecycleResolved, versionOf(t, converted, "news-event:1", 2).Lifecycle,
		"the skater's held reinstatement does not decide the goalie's suspension")
	assert.Empty(t, versionOf(t, converted, "news-event:3", 1).Supersedes)

	events = suspensionAndReinstatement(testReinstatedAt)
	events[1].Event.NeedsReview, events[1].Event.ReviewReason = true, "denied by the team"
	other.Event.NeedsReview = false
	converted = convert(t, append(events, other), testExtraction(goalie, skater))
	for _, e := range converted {
		if e.ID == "news-event:1" {
			assert.Equal(t, LifecycleActive, e.Lifecycle, "the skater's reinstatement does not lift the goalie's penalty")
		}
	}
}
