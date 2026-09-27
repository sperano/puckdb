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
	BirthDate           time.Time
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
	BirthDate    time.Time
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

// TeamSeason is one NHL club's regular-season scoring environment: goals
// and shots on goal it scored, and the power-play opportunities it drew in
// the games that have play-by-play (PowerPlayGames), since opportunities
// are counted from penalty events.
type TeamSeason struct {
	TeamID                 int64
	Abbrev                 string
	Season                 int
	GamesPlayed            int
	GoalsFor               int
	ShotsFor               int
	PowerPlayGames         int
	PowerPlayOpportunities int
}

// SkaterClubSeason is the games a skater played for one club in a season
// split between clubs. SkaterSeason rows name one club per season; these
// credit a split season's games to the clubs they were played for.
type SkaterClubSeason struct {
	PlayerID    int64
	Season      int
	TeamID      int64
	GamesPlayed int
}

// PlayerTeam is the club a player is rostered by for the target season.
type PlayerTeam struct {
	PlayerID int64
	TeamID   int64
}

// TeamEnvironment explains how a skater's team-dependent rates were scaled
// for playing the target season with another club than the ones behind their
// history. From is their most recent history club; OtherClubShare is the
// share of their weighted history games played for clubs other than To.
// Factors multiply the baseline estimates of goals, assists, shots on goal
// and power-play points (points follow goals and assists).
type TeamEnvironment struct {
	FromTeamID     int64            `json:"fromTeamId"`
	FromTeam       string           `json:"fromTeam,omitempty"`
	ToTeamID       int64            `json:"toTeamId"`
	ToTeam         string           `json:"toTeam,omitempty"`
	OtherClubShare float64          `json:"otherClubShare"`
	Factors        map[Stat]float64 `json:"factors"`
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
	// TeamEnvironment is set when a team change scaled the player's
	// team-dependent rates. omitempty keeps the ranking identity of a
	// player without one as it was before the adjustment existed.
	TeamEnvironment *TeamEnvironment `json:",omitempty"`
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
	// TeamSeasons are the clubs' scoring environments, TargetTeams the
	// players' target-season clubs and SkaterClubSeasons the per-club games
	// of split seasons, for the team-environment adjustment.
	TeamSeasons       []TeamSeason
	TargetTeams       []PlayerTeam
	SkaterClubSeasons []SkaterClubSeason
}

func (p PlayerProjection) Value(stat Stat) Estimate {
	return p.Values[stat]
}
