package newsadjust

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/projectionfixtures"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/require"
)

// Shared fixtures for the newsadjust tests. Numbers are synthetic; the
// Hellebuyck-style goalie is a fixture for uncertain availability, not a
// forecast of any real player's missed games.
const (
	testTargetSeason = 20262027
	testSeasonGames  = 82
	testBaselineHash = "baseline-source-hash"
	testGoalieKey    = "nhl:8476945"
	testBackupKey    = "nhl:8480000"
	testSkaterKey    = "nhl:8478402"
	testOtherKey     = "nhl:8479318"
	testLeague1001   = "465.l.1001"
	testLeague1002   = "465.l.1002"
	testEvidenceID   = 101
	testGoalieStarts = 60
	testGoalieShots  = 1800
	testGoalieSaves  = 1650
	testGoalieGA     = 150
	testGoalieWins   = 36
	testGoalieSO     = 4
	testSecondsGame  = 3600
	testSkaterGames  = 80
	testSkaterTOI    = 80 * 20 * 60
	testSkaterGoals  = 40
	testSkaterAssist = 50
	testSkaterPPP    = 30
	testFloatDelta   = 1e-9
)

var (
	testSeasonStart = time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)
	testSeasonEnd   = time.Date(2027, time.April, 15, 0, 0, 0, 0, time.UTC)
	testBaselineAt  = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	testReportedAt  = time.Date(2026, time.September, 10, 15, 0, 0, 0, time.UTC)
	testDraftAt     = time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC)
)

func testSeason() Season {
	return Season{Start: testSeasonStart, End: testSeasonEnd, Games: testSeasonGames}
}

func exact(value float64) projection.Estimate {
	return projection.Estimate{Mean: value, Low: value, High: value}
}

func testGoalie(key string, starts float64) projection.PlayerProjection {
	scale := starts / testGoalieStarts
	shots, saves, ga := testGoalieShots*scale, testGoalieSaves*scale, testGoalieGA*scale
	toi := starts * testSecondsGame
	id := int64(1)
	return projection.PlayerProjection{
		PlayerKey: key, TeamID: &id, Kind: projection.PlayerKindGoalie, Position: "G",
		Source: projection.SourceInternal, SourceAsOf: testBaselineAt, Uncertainty: 0.2,
		Values: map[projection.Stat]projection.Estimate{
			projection.StatGamesPlayed:     exact(starts),
			projection.StatGamesStarted:    exact(starts),
			projection.StatTOISeconds:      exact(toi),
			projection.StatShotsAgainst:    exact(shots),
			projection.StatSaves:           exact(saves),
			projection.StatGoalsAgainst:    exact(ga),
			projection.StatWins:            exact(testGoalieWins * scale),
			projection.StatShutouts:        exact(testGoalieSO * scale),
			projection.StatSavePercentage:  exact(saves / shots),
			projection.StatGoalsAgainstAvg: exact(ga / toi * testSecondsGame),
		},
	}
}

func testSkater(key string) projection.PlayerProjection {
	id := int64(2)
	return projection.PlayerProjection{
		PlayerKey: key, TeamID: &id, Kind: projection.PlayerKindSkater, Position: "C",
		Source: projection.SourceInternal, SourceAsOf: testBaselineAt, Uncertainty: 0.15,
		Values: map[projection.Stat]projection.Estimate{
			projection.StatGamesPlayed:     exact(testSkaterGames),
			projection.StatTOISeconds:      exact(testSkaterTOI),
			projection.StatGoals:           exact(testSkaterGoals),
			projection.StatAssists:         exact(testSkaterAssist),
			projection.StatPoints:          exact(testSkaterGoals + testSkaterAssist),
			projection.StatPowerPlayPoints: exact(testSkaterPPP),
			projection.StatShotsOnGoal:     exact(250),
			projection.StatHits:            exact(40),
			projection.StatBlockedShots:    exact(30),
			projection.StatPenaltyMinutes:  exact(20),
			projection.StatPlusMinus:       {Mean: 5, Low: -5, High: 15},
		},
	}
}

func testBaseline(players ...projection.PlayerProjection) projection.Snapshot {
	return projection.Snapshot{
		TargetSeason: testTargetSeason, AsOf: testBaselineAt, SourceDataHash: testBaselineHash,
		Config: projectionfixtures.Config(), Players: players,
	}
}

func testEvent(id, player string, eventType EventType, duration Duration) Event {
	return Event{
		ID: id, Version: 1, PlayerKey: player, Type: eventType, Status: StatusConfirmed,
		Lifecycle: LifecycleActive, ReportedAt: testReportedAt, RecordedAt: testReportedAt.Add(time.Hour),
		EffectiveFrom: testReportedAt, Duration: duration,
		Evidence: []EvidenceRef{testEvidence(testEvidenceID, news.KindOfficial, testReportedAt)},
	}
}

func testEvidence(versionID int64, kind news.Kind, at time.Time) EvidenceRef {
	return EvidenceRef{
		VersionID: versionID, Publisher: "NHL.com", Kind: kind, URL: "https://www.nhl.com/news/story",
		ReportedAt: at, RetrievedAt: at.Add(time.Minute),
	}
}

func testRequest(baseline projection.Snapshot, events ...Event) Request {
	return Request{Baseline: baseline, Events: events, Policy: DefaultPolicy(), Season: testSeason(), AsOf: testDraftAt}
}

func mustApply(t *testing.T, req Request) Result {
	t.Helper()
	result, err := Apply(req)
	require.NoError(t, err)
	return result
}

func adjustmentFor(t *testing.T, result Result, key string) PlayerAdjustment {
	t.Helper()
	for _, p := range result.Players {
		if p.PlayerKey == key {
			return p
		}
	}
	t.Fatalf("no adjustment for %s", key)
	return PlayerAdjustment{}
}

func playerIn(t *testing.T, snapshot projection.Snapshot, key string) projection.PlayerProjection {
	t.Helper()
	for _, p := range snapshot.Players {
		if p.PlayerKey == key {
			return p
		}
	}
	t.Fatalf("snapshot has no %s", key)
	return projection.PlayerProjection{}
}

func meanIn(t *testing.T, result Result, s Scenario, key string, stat projection.Stat) float64 {
	t.Helper()
	return playerIn(t, result.Snapshots[s], key).Values[stat].Mean
}

func decisionFor(t *testing.T, result Result, eventID string) Decision {
	t.Helper()
	for _, d := range result.Decisions {
		if d.EventID == eventID {
			return d
		}
	}
	t.Fatalf("no decision for %s", eventID)
	return Decision{}
}

func missed(result Result, key string, s Scenario) float64 {
	for _, p := range result.Players {
		if p.PlayerKey == key {
			return p.Effects[s].MissedGames
		}
	}
	return 0
}
