// Package projection builds the draft helper's deterministic baseline
// projections: per-player season estimates (mean and interval) for every
// scoring statistic, generated from regular-season NHL history available at
// an as-of time, merged with the draftable player pool and with imported or
// manual overrides, and stored as immutable snapshots whose config and
// source-data hashes make each one reproducible. Evaluate scores a model
// version against a held-out season.
package projection

import "time"

// PlayerKind separates skater from goalie projections; the two project
// different statistics.
type PlayerKind string

// Player kinds.
const (
	PlayerKindSkater PlayerKind = "skater"
	PlayerKindGoalie PlayerKind = "goalie"
)

// Source says where a player projection's values come from.
type Source string

// Projection sources: SourceInternal is the model's own baseline,
// SourceImported an external provider's row (Provider, ProviderVersion and
// SourceAsOf required) and SourceManual a value entered by hand.
const (
	SourceInternal Source = "internal"
	SourceImported Source = "imported"
	SourceManual   Source = "manual"
)

// Stat names one projected season statistic; Values maps are keyed by it.
type Stat string

// Projected statistics. Skaters project StatGamesPlayed, StatTOISeconds and
// StatGoals through StatFaceoffsLost (faceoffs from nhl-baseline-v2 on);
// goalies StatGamesPlayed through StatTOISeconds and StatWins through
// StatGoalsAgainstAvg.
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

// Estimate is one projected season total: Mean within the interval
// [Low, High], nominally Mean ± Uncertainty × Mean, floored at zero for
// counts and capped at Config.MaxGames for games played.
type Estimate struct {
	Mean float64
	Low  float64
	High float64
}

// SkaterSeason is a skater's regular-season totals for one season, credited
// to one club (TeamID; SkaterClubSeason splits a traded skater's games), with
// the TOI-weighted even-strength points per 60 of their linemates and the
// shared TOI behind it for the linemate correction.
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

// GoalieSeason is a goalie's regular-season totals. GamesPlayed counts every
// boxscore he is listed in, which includes games he dressed for as the
// backup and never entered; GamesAppeared counts only the games he played
// (positive time on ice). nhl-baseline-v6 and later read GamesAppeared;
// earlier versions read GamesPlayed and never see GamesAppeared, which
// omitempty and encodedGoalieHashRows keep out of their hashes.
type GoalieSeason struct {
	PlayerID      int64
	BirthDate     time.Time
	TeamID        int64
	Season        int
	GamesPlayed   int
	GamesAppeared int `json:",omitempty"`
	GamesStarted  int
	TOISeconds    int
	Wins          int
	Shutouts      int
	ShotsAgainst  int
	Saves         int
	GoalsAgainst  int
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

// GoalieStart is one game a goalie started for his club, among the club's
// most recent started games before the projection cutoff (nhl-baseline-v7's
// goalie start share). RecencyRank numbers the club's started games newest
// first over the regular season and playoffs; RegularSeasonRank numbers its
// regular-season games alone and is 0 for a playoff game.
type GoalieStart struct {
	TeamID            int64
	PlayerID          int64
	Season            int
	GameDate          time.Time
	Playoff           bool
	RecencyRank       int
	RegularSeasonRank int
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

// PlayerProjection is one player's row of a snapshot: the estimates, where
// they come from, and the history behind them. HistorySeasons and
// HistoryGames count the seasons and games read, SampleExposure the TOI
// seconds (skaters) or shots against (goalies) they add up to, Uncertainty
// the interval fraction the estimates use, InsufficientHistory a history
// shorter than Config.MinimumHistoryGames, and MissingStats the statistics
// the player's kind should have but Values lacks.
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

// Snapshot is the output of Generate for one target season and as-of time:
// every projected player, sorted by PlayerKey, with the config that produced
// them, the latest game date in the source data, and SourceDataHash over the
// inputs, so an identical rebuild reproduces it.
type Snapshot struct {
	TargetSeason      int
	AsOf              time.Time
	SourceMaxGameDate time.Time
	SourceDataHash    string
	Config            Config
	Players           []PlayerProjection
}

// Input is everything Generate reads. History rows from TargetSeason or
// later are ignored, so an evaluation can pass the held-out season's rows as
// outcomes; ObservedAt is when those outcomes were read.
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
	// GoalieStarts are the clubs' most recent started games, for the goalie
	// start share; only nhl-baseline-v7 and later load or hash them.
	GoalieStarts []GoalieStart
}

// Value returns the estimate for stat, or the zero Estimate when the
// projection has none.
func (p PlayerProjection) Value(stat Stat) Estimate {
	return p.Values[stat]
}
