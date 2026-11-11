package projection

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// seasonWindow is the regular-season range ListProjectionSkaterSeasons and
// ListProjectionGoalieSeasons read, from floor up. A projection window
// (includeTarget false) holds the seasons before target, played by cutoff;
// an evaluation window (includeTarget true) also holds target, the held-out
// season whose rows are the outcomes, and bounds no season by date.
type seasonWindow struct {
	target        int
	floor         int
	includeTarget bool
	cutoff        pgtype.Date
}

// projectionWindow is the history a projection of target as of cutoff reads.
func projectionWindow(target, floor int, cutoff pgtype.Date) seasonWindow {
	return seasonWindow{target: target, floor: floor, cutoff: cutoff}
}

// evaluationWindow is the history and outcomes a held-out evaluation of
// target reads.
func evaluationWindow(target, floor int) seasonWindow {
	return seasonWindow{target: target, floor: floor, includeTarget: true}
}

// projectionLoadWindow is the window PGStore loads a projection of target as
// of asOf from: cfg's lookback floor up to the season before target, played
// by asOf.
func projectionLoadWindow(cfg Config, target int, asOf time.Time) seasonWindow {
	return projectionWindow(target, projectionLoadFloor(cfg, target), dateValue(asOf))
}

// evaluationLoadWindow is the window PGStore.LoadEvaluationInput reads: cfg's
// lookback floor through target, at any date.
func evaluationLoadWindow(cfg Config, target int) seasonWindow {
	return evaluationWindow(target, projectionLoadFloor(cfg, target))
}

func (w seasonWindow) skaterParams() sqlcdb.ListProjectionSkaterSeasonsParams {
	return sqlcdb.ListProjectionSkaterSeasonsParams{
		MinSeason: int32(w.floor), TargetSeason: int32(w.target),
		IncludeTargetSeason: w.includeTarget, GameDateCutoff: w.cutoff,
	}
}

func (w seasonWindow) goalieParams(minimumShutoutTOI int) sqlcdb.ListProjectionGoalieSeasonsParams {
	return sqlcdb.ListProjectionGoalieSeasonsParams{
		MinSeason: int32(w.floor), TargetSeason: int32(w.target),
		IncludeTargetSeason: w.includeTarget, GameDateCutoff: w.cutoff,
		MinimumShutoutToiSeconds: int32(minimumShutoutTOI),
	}
}

// seasonsFromRows converts the season rows of one load, with the skaters'
// linemate context applied.
func seasonsFromRows(
	skaters []sqlcdb.ListProjectionSkaterSeasonsRow,
	linemates []sqlcdb.ListProjectionSkaterLinemateContextRow,
	goalies []sqlcdb.ListProjectionGoalieSeasonsRow,
) ([]SkaterSeason, []GoalieSeason) {
	skaterSeasons := make([]SkaterSeason, 0, len(skaters))
	for _, row := range skaters {
		skaterSeasons = append(skaterSeasons, skaterSeasonFromRow(row))
	}
	applyLinemateContext(skaterSeasons, linemates)
	goalieSeasons := make([]GoalieSeason, 0, len(goalies))
	for _, row := range goalies {
		goalieSeasons = append(goalieSeasons, goalieSeasonFromRow(row))
	}
	return skaterSeasons, goalieSeasons
}

func skaterSeasonFromRow(row sqlcdb.ListProjectionSkaterSeasonsRow) SkaterSeason {
	return SkaterSeason{
		PlayerID: row.PlayerID, BirthDate: optionalDate(row.BirthDate), TeamID: row.TeamID, Season: int(row.Season), Position: string(row.Position),
		GamesPlayed: int(row.GamesPlayed), TOISeconds: int(row.TOISeconds),
		Goals: int(row.Goals), Assists: int(row.Assists), PlusMinus: int(row.PlusMinus),
		PenaltyMinutes: int(row.PenaltyMinutes), PowerPlayPoints: int(row.PowerPlayPoints),
		ShotsOnGoal: int(row.ShotsOnGoal), Hits: int(row.Hits), BlockedShots: int(row.BlockedShots),
		FaceoffsWon: int(row.FaceoffsWon), FaceoffsLost: int(row.FaceoffsLost),
	}
}

func goalieSeasonFromRow(row sqlcdb.ListProjectionGoalieSeasonsRow) GoalieSeason {
	return GoalieSeason{
		PlayerID: row.PlayerID, BirthDate: optionalDate(row.BirthDate), TeamID: row.TeamID, Season: int(row.Season), GamesPlayed: int(row.GamesPlayed),
		GamesAppeared: int(row.GamesAppeared), GamesStarted: int(row.GamesStarted), TOISeconds: int(row.TOISeconds),
		Wins: int(row.Wins), Shutouts: int(row.Shutouts), ShotsAgainst: int(row.ShotsAgainst),
		Saves: int(row.Saves), GoalsAgainst: int(row.GoalsAgainst),
	}
}
