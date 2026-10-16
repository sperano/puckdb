package draftrank_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staleNow is past serviceStaleAfter for both the fixture snapshot and its
// pool: the 26-hour-old ranking of the live-board review finding.
var staleNow = draftfixtures.AsOf.Add(26 * time.Hour)

func changeRules(store *draftfixtures.Store, change func(*draft.Rules)) {
	key := draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)
	rules := store.Rules[key]
	rules.Rules.RosterSlots = append([]draft.RosterSlot(nil), rules.Rules.RosterSlots...)
	rules.Rules.Categories = append([]draft.StatCategory(nil), rules.Rules.Categories...)
	change(&rules.Rules)
	store.Rules[key] = rules
}

func setRefresh(store *draftfixtures.Store, refresh draftrank.Refresh) {
	store.Refreshes[draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)] = &refresh
}

func TestRead_ReportsDynamicRankingConditions(t *testing.T) {
	afterSnapshot := draftfixtures.AsOf.Add(time.Minute)
	tests := []struct {
		name   string
		now    time.Time
		setup  func(*draftfixtures.Store)
		codes  []draftrank.IssueCode
		advice draftrank.Advice
	}{
		{name: "current", now: serviceNow, setup: func(*draftfixtures.Store) {},
			codes: nil, advice: draftrank.AdviceUnaffected},
		{name: "stale snapshot and pool", now: staleNow, setup: func(*draftfixtures.Store) {},
			codes: []draftrank.IssueCode{draftrank.IssueStaleSnapshot, draftrank.IssueStalePool}, advice: draftrank.AdviceOutdated},
		{name: "changed overrides", now: serviceNow, setup: func(s *draftfixtures.Store) { s.Changes = 1 },
			codes: []draftrank.IssueCode{draftrank.IssueOverridesChanged}, advice: draftrank.AdviceOutdated},
		{name: "changed roster slots", now: serviceNow, setup: func(s *draftfixtures.Store) {
			changeRules(s, func(r *draft.Rules) { r.RosterSlots[0].Count++ })
		}, codes: []draftrank.IssueCode{draftrank.IssueRulesChanged, draftrank.IssueRosterSlotsChanged}, advice: draftrank.AdviceWithheld},
		{name: "changed category weight", now: serviceNow, setup: func(s *draftfixtures.Store) {
			changeRules(s, func(r *draft.Rules) { weight := 5.0; r.Categories[0].Weight = &weight })
		}, codes: []draftrank.IssueCode{draftrank.IssueRulesChanged, draftrank.IssueScoringChanged}, advice: draftrank.AdviceWithheld},
		{name: "changed draft status only", now: serviceNow, setup: func(s *draftfixtures.Store) {
			changeRules(s, func(r *draft.Rules) { r.Draft.Status = "draft"; r.Name = "Renamed League" })
		}, codes: []draftrank.IssueCode{draftrank.IssueRulesChanged}, advice: draftrank.AdviceOutdated},
		{name: "failed refresh after the snapshot", now: serviceNow, setup: func(s *draftfixtures.Store) {
			setRefresh(s, draftrank.Refresh{State: draftrank.RefreshFailed, Code: draftrank.IssueMissingPool,
				Error: "pool is empty", StartedAt: afterSnapshot})
		}, codes: []draftrank.IssueCode{draftrank.IssueRefreshFailed}, advice: draftrank.AdviceOutdated},
		{name: "refresh running after the snapshot", now: serviceNow, setup: func(s *draftfixtures.Store) {
			setRefresh(s, draftrank.Refresh{State: draftrank.RefreshRunning, StartedAt: serviceNow})
		}, codes: []draftrank.IssueCode{draftrank.IssueRefreshRunning}, advice: draftrank.AdviceUnaffected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := draftfixtures.NewStore()
			snapshot := fixtureSnapshot(t)
			store.Add(snapshot)
			test.setup(store)

			read, err := newService(store, test.now).Read(context.Background(), fixtureRef(), uuid.Nil)

			require.NoError(t, err)
			assert.Equal(t, draftrank.StatusReady, read.Status)
			require.NotNil(t, read.Snapshot)
			assert.Equal(t, snapshot.ID, read.Snapshot.ID)
			want := append([]draftrank.IssueCode{draftrank.IssueNewsSourceStale}, test.codes...)
			assert.ElementsMatch(t, want, issueCodes(read.Issues))
			assert.Equal(t, test.advice, draftrank.AdviceOf(read.Issues))
			assert.Equal(t, draftrank.RulesHash(store.Rules[draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)]),
				read.RulesHash, "the read names the current rules its issues were checked against")
		})
	}
}

func TestRead_RosterSlotsChangedNamesBothSlotSets(t *testing.T) {
	store := draftfixtures.NewStore()
	store.Add(fixtureSnapshot(t))
	changeRules(store, func(r *draft.Rules) { r.RosterSlots[0].Count = 2 })

	read, err := newService(store, serviceNow).Read(context.Background(), fixtureRef(), uuid.Nil)

	require.NoError(t, err)
	assert.Contains(t, read.Issues, draftrank.Issue{Code: draftrank.IssueRosterSlotsChanged,
		Message: "roster slots are now C×2, LW×1, RW×1, G×1 but this snapshot was ranked with C×1, LW×1, RW×1, G×1; recommendations are withheld until a refresh"})
}

func TestRankings_ReportsTheSharedReadIssues(t *testing.T) {
	store := draftfixtures.NewStore()
	store.Add(fixtureSnapshot(t))
	store.Changes = 3
	changeRules(store, func(r *draft.Rules) { r.RosterSlots[0].Count++ })
	setRefresh(store, draftrank.Refresh{State: draftrank.RefreshCanceled, StartedAt: staleNow.Add(-time.Hour)})
	svc := newService(store, staleNow)

	read, err := svc.Read(context.Background(), fixtureRef(), uuid.Nil)
	require.NoError(t, err)
	page, err := svc.Rankings(context.Background(), fixtureRef(), uuid.Nil, draftrank.Query{})
	require.NoError(t, err)

	assert.Equal(t, read.Issues, page.Issues, "the rankings API serves exactly the shared read's issues")
	assert.Equal(t, read.Status, page.Status)
	assert.Equal(t, read.Snapshot.ID, page.Snapshot.ID)
}

func TestRead_WithoutSnapshotReportsStatusAndCurrentRules(t *testing.T) {
	store := draftfixtures.NewStore()
	setRefresh(store, draftrank.Refresh{State: draftrank.RefreshFailed, Code: draftrank.IssueMissingPool, StartedAt: serviceNow})

	read, err := newService(store, serviceNow).Read(context.Background(), fixtureRef(), uuid.Nil)

	require.NoError(t, err)
	assert.Nil(t, read.Snapshot)
	assert.Equal(t, draftrank.StatusFailed, read.Status)
	assert.Equal(t, []draftrank.IssueCode{draftrank.IssueRefreshFailed}, issueCodes(read.Issues))
	assert.Equal(t, draftrank.RulesHash(draftfixtures.Rules()), read.RulesHash)
	assert.Equal(t, draftfixtures.LeagueKey, read.League.LeagueKey)
}
