// Package draftfixtures builds a synthetic draft ranking snapshot and an
// in-memory store for the draftrank, GraphQL and CLI tests, so every layer
// is tested against the same data. The league, players and projections are
// invented; the suspension is a fixture for news-driven rank changes, not a
// forecast. Only tests import this package.
package draftfixtures

import (
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/sperano/puckdb/internal/projection"
)

// Fixture identities.
const (
	Season       = 2026
	LeagueID     = 1001
	LeagueKey    = "465.l.1001"
	OtherLeague  = 1002
	OtherKey     = "465.l.1002"
	LeagueName   = "Fixture League"
	TopCenter    = "465.p.1"
	CenterWing   = "465.p.2"
	LeftWing     = "465.p.3"
	DepthCenter  = "465.p.4"
	AccentWing   = "465.p.5"
	StarterG     = "465.p.6"
	BackupG      = "465.p.7"
	RightWing    = "465.p.8"
	SuspendedKey = TopCenter
	// AccentName is a name that only matches an accent-insensitive search.
	AccentName = "Tim Stützle"
	// NewsSourceID is the fixture's stale news source.
	NewsSourceID = "nhl-news"
)

const (
	nhlSeason        = 20262027
	goalsStat        = 1
	assistsStat      = 2
	winsStat         = 19
	goalWeight       = 2
	assistWeight     = 1
	winWeight        = 3
	numTeams         = 2
	seasonGames      = 82
	suspensionGames  = 20
	skaterGames      = 80
	secondsPerGame   = 20 * 60
	goalieSecondsPer = 3600
	uncertainty      = 0.15
	evidenceID       = 101
	staleHours       = 72
)

var (
	// BaselineAt is the projection cutoff; AsOf the snapshot time.
	BaselineAt  = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	ReportedAt  = time.Date(2026, time.September, 10, 15, 0, 0, 0, time.UTC)
	AsOf        = time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	FetchedAt   = time.Date(2026, time.September, 20, 6, 0, 0, 0, time.UTC)
	seasonStart = time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)
	seasonEnd   = time.Date(2027, time.April, 15, 0, 0, 0, 0, time.UTC)
)

type fixturePlayer struct {
	key, name, team string
	positions       []string
	goals, assists  float64
	wins            float64
}

var fixturePlayers = []fixturePlayer{
	{key: TopCenter, name: "Connor Top", team: "EDM", positions: []string{draft.PositionCenter}, goals: 40, assists: 50},
	{key: CenterWing, name: "Dual Eligible", team: "TOR", positions: []string{draft.PositionCenter, draft.PositionLeftWing}, goals: 35, assists: 45},
	{key: LeftWing, name: "Left Winger", team: "BOS", positions: []string{draft.PositionLeftWing}, goals: 25, assists: 20},
	{key: DepthCenter, name: "Depth Center", team: "MTL", positions: []string{draft.PositionCenter}, goals: 20, assists: 20},
	{key: AccentWing, name: AccentName, team: "OTT", positions: []string{draft.PositionLeftWing}, goals: 10, assists: 15},
	{key: StarterG, name: "Starting Goalie", team: "WPG", positions: []string{draft.PositionGoalie}, wins: 36},
	{key: BackupG, name: "Backup Goalie", team: "NYR", positions: []string{draft.PositionGoalie}, wins: 15},
	{key: RightWing, name: "Right Winger", team: "TBL", positions: []string{draft.PositionRightWing}, goals: 18, assists: 12},
}

// Rules is the fixture league's rules: a points league scoring goals,
// assists and goalie wins with one C, LW, RW and G per team.
func Rules() draft.Snapshot {
	weight := func(w float64) *float64 { return &w }
	return draft.Snapshot{
		Rules: draft.Rules{
			LeagueKey: LeagueKey, GameKey: 465, LeagueID: LeagueID, Season: Season, Name: LeagueName,
			ScoringType: "point", NumTeams: numTeams,
			Categories: []draft.StatCategory{
				{StatID: goalsStat, Name: "Goals", Abbr: "G", PositionTypes: []string{"P"}, Weight: weight(goalWeight), Enabled: true},
				{StatID: assistsStat, Name: "Assists", Abbr: "A", PositionTypes: []string{"P"}, Weight: weight(assistWeight), Enabled: true},
				{StatID: winsStat, Name: "Wins", Abbr: "W", PositionTypes: []string{"G"}, Weight: weight(winWeight), Enabled: true},
			},
			RosterSlots: []draft.RosterSlot{
				{Position: draft.PositionCenter, Count: 1, Starting: true},
				{Position: draft.PositionLeftWing, Count: 1, Starting: true},
				{Position: draft.PositionRightWing, Count: 1, Starting: true},
				{Position: draft.PositionGoalie, Count: 1, Starting: true},
			},
		},
		Source: draft.SourceYahooAPI, FetchedAt: FetchedAt,
	}
}

// Pool is the fixture league's draftable pool.
func Pool() []draft.PoolPlayer {
	pool := make([]draft.PoolPlayer, 0, len(fixturePlayers))
	for i, p := range fixturePlayers {
		pool = append(pool, draft.PoolPlayer{
			YahooPlayerID: i + 1, PlayerKey: p.key, Name: p.name, Team: p.team,
			EligiblePositions: p.positions, NHLPlayerID: int64(8470000 + i), FetchedAt: FetchedAt,
		})
	}
	return pool
}

func exact(value float64) projection.Estimate {
	return projection.Estimate{Mean: value, Low: value, High: value}
}

// Baseline is the fixture's projection snapshot.
func Baseline() projection.Snapshot {
	players := make([]projection.PlayerProjection, 0, len(fixturePlayers))
	for _, p := range fixturePlayers {
		projected := projection.PlayerProjection{
			PlayerKey: p.key, Kind: projection.PlayerKindSkater, Position: p.positions[0], Source: projection.SourceInternal,
			SourceAsOf: BaselineAt, Uncertainty: uncertainty,
			Values: map[projection.Stat]projection.Estimate{
				projection.StatGamesPlayed: exact(skaterGames), projection.StatTOISeconds: exact(skaterGames * secondsPerGame),
				projection.StatGoals: exact(p.goals), projection.StatAssists: exact(p.assists),
				projection.StatPoints: exact(p.goals + p.assists),
			},
		}
		if p.positions[0] == draft.PositionGoalie {
			projected.Kind = projection.PlayerKindGoalie
			projected.Values = map[projection.Stat]projection.Estimate{
				projection.StatGamesPlayed: exact(p.wins * 2), projection.StatGamesStarted: exact(p.wins * 2),
				projection.StatTOISeconds: exact(p.wins * 2 * goalieSecondsPer), projection.StatWins: exact(p.wins),
			}
		}
		players = append(players, projected)
	}
	return projection.Snapshot{
		TargetSeason: nhlSeason, AsOf: BaselineAt, SourceDataHash: "fixture-baseline", Config: projection.DefaultConfig(), Players: players,
	}
}

// Adjustment applies a confirmed 20-game suspension of the top center.
func Adjustment() (newsadjust.Result, error) {
	event := newsadjust.Event{
		ID: "news-event:1", Version: 1, PlayerKey: SuspendedKey, Type: newsadjust.EventSuspension,
		Status: newsadjust.StatusConfirmed, Lifecycle: newsadjust.LifecycleActive,
		ReportedAt: ReportedAt, RecordedAt: ReportedAt.Add(time.Hour), EffectiveFrom: ReportedAt,
		Duration: newsadjust.Duration{Kind: newsadjust.DurationGames, Games: suspensionGames},
		Evidence: []newsadjust.EvidenceRef{{
			VersionID: evidenceID, Publisher: "NHL.com", Kind: news.KindOfficial, URL: "https://www.nhl.com/news/story",
			ReportedAt: ReportedAt, RetrievedAt: ReportedAt.Add(time.Minute), Quote: "suspended for 20 games",
		}},
	}
	return newsadjust.Apply(newsadjust.Request{
		Baseline: Baseline(), Events: []newsadjust.Event{event}, Policy: newsadjust.DefaultPolicy(),
		Season: newsadjust.Season{Start: seasonStart, End: seasonEnd, Games: seasonGames}, AsOf: AsOf, LeagueKey: LeagueKey,
	})
}

// News is the fixture's news coverage: one source stale at AsOf.
func News() []news.SourceCoverage {
	return []news.SourceCoverage{{
		Source: news.Source{ID: NewsSourceID, Publisher: "NHL.com"}, Scope: "2026", Status: news.CoverageStale,
		AsOf:  AsOf.Add(-staleHours * time.Hour),
		State: news.FetchState{SourceID: NewsSourceID, LastSuccessAt: AsOf.Add(-staleHours * time.Hour)},
	}}
}

// BuildInput is the complete input of the fixture snapshot.
func BuildInput() (draftrank.BuildInput, error) {
	adjustment, err := Adjustment()
	if err != nil {
		return draftrank.BuildInput{}, err
	}
	return draftrank.BuildInput{
		Rules: Rules(), Pool: Pool(), Baseline: Baseline(), BaselineID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("baseline")),
		Adjustment: &adjustment, AdjustmentRunID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("run")),
		Options: draftrank.Options{BenchPolicy: string(draft.BenchIncluded)}, News: News(), AsOf: AsOf,
	}, nil
}

// Snapshot builds the fixture snapshot and gives it a stored identity.
func Snapshot(id uuid.UUID, createdAt time.Time) (*draftrank.Snapshot, error) {
	input, err := BuildInput()
	if err != nil {
		return nil, err
	}
	snapshot, err := draftrank.Build(input)
	if err != nil {
		return nil, err
	}
	snapshot.ID, snapshot.CreatedAt = id, createdAt
	return &snapshot, nil
}
