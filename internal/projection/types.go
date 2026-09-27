package projection

import "time"

type PlayerKind string

const (
	PlayerKindSkater PlayerKind = "skater"
	PlayerKindGoalie PlayerKind = "goalie"
)

type Source string

const (
	SourceInternal Source = "internal"
	SourceImported Source = "imported"
	SourceManual   Source = "manual"
)

type Stat string

const (
	StatGamesPlayed     Stat = "games_played"
	StatGamesStarted    Stat = "games_started"
	StatTOISeconds      Stat = "toi_seconds"
	StatGoals           Stat = "goals"
	StatAssists         Stat = "assists"
	StatPoints          Stat = "points"
	StatPlusMinus       Stat = "plus_minus"
	StatPenaltyMinutes  Stat = "penalty_minutes"
	StatPowerPlayPoints Stat = "power_play_points"
	StatShotsOnGoal     Stat = "shots_on_goal"
	StatHits            Stat = "hits"
	StatBlockedShots    Stat = "blocked_shots"
	StatFaceoffsWon     Stat = "faceoffs_won"
	StatFaceoffsLost    Stat = "faceoffs_lost"
	StatWins            Stat = "wins"
	StatShutouts        Stat = "shutouts"
	StatShotsAgainst    Stat = "shots_against"
	StatSaves           Stat = "saves"
	StatGoalsAgainst    Stat = "goals_against"
	StatSavePercentage  Stat = "save_percentage"
	StatGoalsAgainstAvg Stat = "goals_against_average"
)

type Estimate struct {
	Mean float64
	Low  float64
	High float64
}

type SkaterSeason struct {
	PlayerID            int64
	TeamID              int64
	Season              int
	Position            string
	GamesPlayed         int
	TOISeconds          int
	Goals               int
	Assists             int
	PlusMinus           int
	PenaltyMinutes      int
	PowerPlayPoints     int
	ShotsOnGoal         int
	Hits                int
	BlockedShots        int
	FaceoffsWon         int
	FaceoffsLost        int
	LinematePointsPer60 float64
	LinemateTOISeconds  int
}

// LinemateContext records the historical even-strength context correction
// used for a skater projection. Rates are TOI-weighted points per 60 minutes.
type LinemateContext struct {
	ObservedPointsPer60 float64
	AveragePointsPer60  float64
	SharedTOISeconds    float64
	AdjustmentFactor    float64
}

type GoalieSeason struct {
	PlayerID     int64
	TeamID       int64
	Season       int
	GamesPlayed  int
	GamesStarted int
	TOISeconds   int
	Wins         int
	Shutouts     int
	ShotsAgainst int
	Saves        int
	GoalsAgainst int
}

// Override supplies projection values for a rookie, an unresolved player, or
// an external provider row. Values replace an internal baseline for the
// same PlayerKey, while the original baseline remains reproducible in its own
// snapshot.
type Override struct {
	PlayerKey               string
	PlayerID                *int64
	TeamID                  *int64
	Kind                    PlayerKind
	Position                string
	Source                  Source
	Provider                string
	ProviderVersion         string
	SourceAsOf              time.Time
	IncorporatesNewsThrough time.Time
	Values                  map[Stat]Estimate
}

type PlayerProjection struct {
	PlayerKey               string
	PlayerID                *int64
	TeamID                  *int64
	Kind                    PlayerKind
	Position                string
	Source                  Source
	Provider                string
	ProviderVersion         string
	SourceAsOf              time.Time
	IncorporatesNewsThrough time.Time
	HistorySeasons          int
	HistoryGames            int
	SampleExposure          float64
	Uncertainty             float64
	InsufficientHistory     bool
	MissingStats            []Stat
	LinemateContext         *LinemateContext
	Values                  map[Stat]Estimate
}

// PoolPlayer identifies a draftable player independently of NHL history.
// Players without usable history remain in a snapshot with explicit missing
// stats so a rookie or unresolved match cannot disappear silently.
type PoolPlayer struct {
	PlayerKey string
	PlayerID  *int64
	TeamID    *int64
	Kind      PlayerKind
	Position  string
}

// LeagueCategory is an enabled Yahoo scoring category that a snapshot must
// support. Display-only categories are intentionally ignored by coverage.
type LeagueCategory struct {
	LeagueID    int
	StatID      int
	Name        string
	DisplayOnly bool
}

type Snapshot struct {
	TargetSeason      int
	AsOf              time.Time
	SourceMaxGameDate time.Time
	SourceDataHash    string
	Config            Config
	Players           []PlayerProjection
}

type Input struct {
	TargetSeason      int
	AsOf              time.Time
	ObservedAt        time.Time
	SourceMaxGameDate time.Time
	Skaters           []SkaterSeason
	Goalies           []GoalieSeason
	PlayerPool        []PoolPlayer
	Overrides         []Override
}

func (p PlayerProjection) Value(stat Stat) Estimate {
	return p.Values[stat]
}
