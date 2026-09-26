package draftrank_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const serviceStaleAfter = 24 * time.Hour

var serviceNow = draftfixtures.AsOf.Add(time.Hour)

func newService(store *draftfixtures.Store, now time.Time) *draftrank.Service {
	return draftrank.NewService(store, store, draftrank.ServiceOptions{StaleAfter: serviceStaleAfter, Now: func() time.Time { return now }})
}

func fixtureRef() draftrank.LeagueRef {
	return draftrank.LeagueRef{Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID}
}

func issueCodes(issues []draftrank.Issue) []draftrank.IssueCode {
	codes := make([]draftrank.IssueCode, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

func TestService_ServesLatestSnapshotWithVersionAndFreshness(t *testing.T) {
	store := draftfixtures.NewStore()
	snapshot := fixtureSnapshot(t)
	store.Add(snapshot)
	svc := newService(store, serviceNow)

	page, err := svc.Rankings(context.Background(), draftrank.LeagueRef{LeagueKey: draftfixtures.LeagueKey}, uuid.Nil, draftrank.Query{Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, draftrank.StatusReady, page.Status)
	require.NotNil(t, page.Snapshot)
	assert.Equal(t, snapshot.ID, page.Snapshot.ID)
	assert.Equal(t, snapshot.Identity, page.Snapshot.Identity)
	assert.Equal(t, snapshot.ID, page.LatestSnapshotID)
	assert.Equal(t, len(snapshot.Players), page.Total)
	assert.Len(t, page.Rows, 2)
	assert.Equal(t, []draftrank.IssueCode{draftrank.IssueNewsSourceStale}, issueCodes(page.Issues))
}

func TestService_ReportsEveryConditionOfAServedSnapshot(t *testing.T) {
	store := draftfixtures.NewStore()
	old := fixtureSnapshot(t)
	store.Add(old)
	newer := *old
	newer.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("newer"))
	store.Add(&newer)
	store.Changes = 2
	store.Refreshes[draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)] = &draftrank.Refresh{
		State: draftrank.RefreshFailed, Code: draftrank.IssueMissingProjections, Error: "3 players lack projections",
		StartedAt: draftfixtures.AsOf.Add(time.Hour),
	}
	rulesKey := draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)
	changed := store.Rules[rulesKey]
	changed.Rules.NumTeams++
	store.Rules[rulesKey] = changed
	later := draftfixtures.AsOf.Add(3 * serviceStaleAfter)
	svc := newService(store, later)

	page, err := svc.Rankings(context.Background(), fixtureRef(), old.ID, draftrank.Query{Scenario: draftrank.ScenarioConservative})
	require.NoError(t, err)
	assert.Equal(t, old.ID, page.Snapshot.ID, "a pinned snapshot is served even when a newer one exists")
	assert.Equal(t, newer.ID, page.LatestSnapshotID)
	assert.Equal(t, draftrank.ScenarioConservative, page.Scenario)
	assert.ElementsMatch(t, []draftrank.IssueCode{
		draftrank.IssueNewsSourceStale, draftrank.IssueStaleSnapshot, draftrank.IssueStalePool,
		draftrank.IssueOverridesChanged, draftrank.IssueRulesChanged, draftrank.IssueNewerSnapshot, draftrank.IssueRefreshFailed,
	}, issueCodes(page.Issues))
}

func TestService_StatesWithoutSnapshot(t *testing.T) {
	key := draftfixtures.Key(draftfixtures.Season, draftfixtures.LeagueID)
	tests := []struct {
		name    string
		refresh *draftrank.Refresh
		rules   bool
		scoring string
		status  draftrank.Status
		codes   []draftrank.IssueCode
	}{
		{"never refreshed", nil, true, "point", draftrank.StatusNotComputed, []draftrank.IssueCode{draftrank.IssueNotComputed}},
		{"first refresh running", &draftrank.Refresh{State: draftrank.RefreshRunning, StartedAt: serviceNow}, true, "point",
			draftrank.StatusRefreshing, []draftrank.IssueCode{draftrank.IssueRefreshRunning}},
		{"interrupted refresh", &draftrank.Refresh{State: draftrank.RefreshRunning, StartedAt: serviceNow.Add(-2 * draftrank.RefreshTimeout)}, true, "point",
			draftrank.StatusFailed, []draftrank.IssueCode{draftrank.IssueRefreshInterrupted}},
		{"failed refresh", &draftrank.Refresh{State: draftrank.RefreshFailed, Code: draftrank.IssueMissingPool}, true, "point",
			draftrank.StatusFailed, []draftrank.IssueCode{draftrank.IssueRefreshFailed}},
		{"canceled refresh", &draftrank.Refresh{State: draftrank.RefreshCanceled}, true, "point",
			draftrank.StatusFailed, []draftrank.IssueCode{draftrank.IssueRefreshCanceled}},
		{"missing rules", nil, false, "", draftrank.StatusNotComputed, []draftrank.IssueCode{draftrank.IssueMissingRules, draftrank.IssueNotComputed}},
		{"unsupported scoring", nil, true, "vegas", draftrank.StatusNotComputed, []draftrank.IssueCode{draftrank.IssueUnsupportedScoring, draftrank.IssueNotComputed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := draftfixtures.NewStore()
			if tt.refresh != nil {
				store.Refreshes[key] = tt.refresh
			}
			if !tt.rules {
				delete(store.Rules, key)
			} else {
				rules := store.Rules[key]
				rules.Rules.ScoringType = tt.scoring
				store.Rules[key] = rules
			}
			page, err := newService(store, serviceNow).Rankings(context.Background(), fixtureRef(), uuid.Nil, draftrank.Query{})
			require.NoError(t, err)
			assert.Equal(t, tt.status, page.Status)
			assert.Equal(t, tt.codes, issueCodes(page.Issues))
			assert.Nil(t, page.Snapshot)
			assert.NotNil(t, page.Rows)
			assert.Empty(t, page.Rows)
		})
	}
}

func TestService_RejectsForeignSnapshotsAndUnknownLeagues(t *testing.T) {
	store := draftfixtures.NewStore()
	snapshot := fixtureSnapshot(t)
	store.Add(snapshot)
	svc := newService(store, serviceNow)

	_, err := svc.Rankings(context.Background(), draftrank.LeagueRef{Season: draftfixtures.Season, LeagueID: draftfixtures.OtherLeague}, snapshot.ID, draftrank.Query{})
	assert.ErrorIs(t, err, draftrank.ErrInvalidQuery)
	_, err = svc.Rankings(context.Background(), draftrank.LeagueRef{LeagueKey: "465.l.1"}, uuid.Nil, draftrank.Query{})
	assert.ErrorIs(t, err, draftrank.ErrUnknownLeague)
	_, err = svc.Rankings(context.Background(), fixtureRef(), uuid.New(), draftrank.Query{})
	assert.ErrorIs(t, err, draftrank.ErrSnapshotNotFound)
	_, err = svc.Rankings(context.Background(), fixtureRef(), uuid.Nil, draftrank.Query{Positions: []string{"X"}})
	assert.ErrorIs(t, err, draftrank.ErrInvalidQuery)
}

func TestService_CachesDecodedSnapshots(t *testing.T) {
	store := draftfixtures.NewStore()
	store.Add(fixtureSnapshot(t))
	svc := newService(store, serviceNow)
	for range 3 {
		_, err := svc.Rankings(context.Background(), fixtureRef(), uuid.Nil, draftrank.Query{Positions: []string{"G"}})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, store.LoadCalls)
}

func TestService_LeaguesListsConfiguredAndImportedLeagues(t *testing.T) {
	store := draftfixtures.NewStore()
	store.Add(fixtureSnapshot(t))
	summaries, err := newService(store, serviceNow).Leagues(context.Background(), draftfixtures.Season, []int{draftfixtures.OtherLeague, draftfixtures.LeagueID})
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	assert.Equal(t, draftfixtures.LeagueID, summaries[0].League.LeagueID)
	assert.Equal(t, draftrank.StatusReady, summaries[0].Status)
	assert.NotNil(t, summaries[0].Snapshot)
	assert.Equal(t, draftfixtures.OtherLeague, summaries[1].League.LeagueID)
	assert.Equal(t, draftrank.StatusNotComputed, summaries[1].Status)
	assert.Contains(t, issueCodes(summaries[1].Issues), draftrank.IssueMissingRules)
}
