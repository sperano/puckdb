package evenstrength

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Store is the subset of sqlcdb.Queries a rebuild uses; *sqlcdb.Queries
// satisfies it.
type Store interface {
	ListEvenStrengthShiftsForGame(ctx context.Context, gameID int64) ([]sqlcdb.ListEvenStrengthShiftsForGameRow, error)
	ListEvenStrengthGoalsForGame(ctx context.Context, gameID int64) ([]sqlcdb.ListEvenStrengthGoalsForGameRow, error)
	DeleteEvenStrengthPairTOIForGame(ctx context.Context, gameID int64) error
	DeleteEvenStrengthSkaterGamesForGame(ctx context.Context, gameID int64) error
	InsertEvenStrengthPairTOI(ctx context.Context, arg sqlcdb.InsertEvenStrengthPairTOIParams) (int64, error)
	InsertEvenStrengthSkaterGames(ctx context.Context, arg sqlcdb.InsertEvenStrengthSkaterGamesParams) (int64, error)
}

// Rebuild replaces one game's even_strength_pair_toi and
// even_strength_skater_games rows with the totals derived from its stored
// shifts, box-score rows and goals, and returns the number of rows inserted
// across both tables. Run it inside one transaction so readers never see a
// half-built game; it is idempotent, so a retry rebuilds the same rows.
func Rebuild(ctx context.Context, q Store, gameID int64) (int64, error) {
	shiftRows, err := q.ListEvenStrengthShiftsForGame(ctx, gameID)
	if err != nil {
		return 0, fmt.Errorf("list shifts: %w", err)
	}
	goalRows, err := q.ListEvenStrengthGoalsForGame(ctx, gameID)
	if err != nil {
		return 0, fmt.Errorf("list goals: %w", err)
	}
	totals := Derive(shiftsFromRows(shiftRows), goalsFromRows(goalRows))

	if err := q.DeleteEvenStrengthPairTOIForGame(ctx, gameID); err != nil {
		return 0, fmt.Errorf("delete pair toi: %w", err)
	}
	if err := q.DeleteEvenStrengthSkaterGamesForGame(ctx, gameID); err != nil {
		return 0, fmt.Errorf("delete skater games: %w", err)
	}
	pairs, err := q.InsertEvenStrengthPairTOI(ctx, pairParams(gameID, totals.Pairs))
	if err != nil {
		return 0, fmt.Errorf("insert pair toi: %w", err)
	}
	skaters, err := q.InsertEvenStrengthSkaterGames(ctx, skaterParams(gameID, totals.Skaters))
	if err != nil {
		return 0, fmt.Errorf("insert skater games: %w", err)
	}
	return pairs + skaters, nil
}

func shiftsFromRows(rows []sqlcdb.ListEvenStrengthShiftsForGameRow) []Shift {
	shifts := make([]Shift, len(rows))
	for i, r := range rows {
		shifts[i] = Shift{
			PlayerID: r.PlayerID,
			TeamID:   r.TeamID,
			Period:   r.Period,
			Start:    r.StartTime,
			End:      r.EndTime,
			IsSkater: r.IsSkater,
		}
	}
	return shifts
}

func goalsFromRows(rows []sqlcdb.ListEvenStrengthGoalsForGameRow) []Goal {
	goals := make([]Goal, len(rows))
	for i, r := range rows {
		goals[i] = Goal{Period: r.Period, Clock: r.TimeInPeriod}
		if r.SituationCode.Valid {
			situation := r.SituationCode.Int32
			goals[i].Situation = &situation
		}
		for _, id := range []pgtype.Int8{r.ScoringPlayerID, r.Assist1PlayerID, r.Assist2PlayerID} {
			if id.Valid {
				goals[i].Credited = append(goals[i].Credited, id.Int64)
			}
		}
	}
	return goals
}

func pairParams(gameID int64, pairs []PairTOI) sqlcdb.InsertEvenStrengthPairTOIParams {
	p := sqlcdb.InsertEvenStrengthPairTOIParams{
		GameID:           gameID,
		PlayerIds:        make([]int64, len(pairs)),
		TeammateIds:      make([]int64, len(pairs)),
		SharedToiSeconds: make([]int32, len(pairs)),
	}
	for i, pair := range pairs {
		p.PlayerIds[i] = pair.PlayerID
		p.TeammateIds[i] = pair.TeammateID
		p.SharedToiSeconds[i] = pair.SharedSeconds
	}
	return p
}

func skaterParams(gameID int64, skaters []SkaterGame) sqlcdb.InsertEvenStrengthSkaterGamesParams {
	p := sqlcdb.InsertEvenStrengthSkaterGamesParams{
		GameID:     gameID,
		PlayerIds:  make([]int64, len(skaters)),
		TeamIds:    make([]int64, len(skaters)),
		TOISeconds: make([]int32, len(skaters)),
		Points:     make([]int32, len(skaters)),
	}
	for i, s := range skaters {
		p.PlayerIds[i] = s.PlayerID
		p.TeamIds[i] = s.TeamID
		p.TOISeconds[i] = s.TOISeconds
		p.Points[i] = s.Points
	}
	return p
}
