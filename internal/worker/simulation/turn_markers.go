package simulation

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Turn markers are the sim_transactions rows whose presence means a turn
// already committed: the draft_pick of a draft slot and the
// daily_turn_done of a (pool, agent, day). DraftPick and ManageRoster
// probe for theirs before calling the model, but that probe runs outside
// the commit, so two overlapping attempts (one still running past its
// StartToClose timeout, the other Temporal's retry) can both pass it. The
// partial unique indexes below make the commit itself the guard: each
// commit inserts its marker before any other write, so the losing
// attempt waits on the winner's index entry, fails with a unique
// violation once the winner commits, and rolls back without having
// written a roster row, an action or the turn telemetry (whose
// delete-then-insert would otherwise replace the winner's turn). The
// loser then records its spend (recordLostTurn) and returns the result
// of the committed turn.
const (
	// draftPickMarkerIndex is the unique index on (pool_id, agent_id,
	// round, pick) of the draft_pick rows.
	draftPickMarkerIndex = "ux_sim_transactions_draft_pick"
	// dailyTurnMarkerIndex is the unique index on (pool_id, agent_id,
	// date) of the daily_turn_done rows.
	dailyTurnMarkerIndex = "ux_sim_transactions_daily_turn_done"

	// pgUniqueViolation is the SQLSTATE of a unique constraint violation.
	pgUniqueViolation = "23505"
)

// errTurnCommittedConcurrently is the error detail of a turn whose
// commit lost its marker to an overlapping attempt.
var errTurnCommittedConcurrently = errors.New("simulation: turn already committed by an overlapping attempt")

// isMarkerConflict reports whether err is a unique violation of the
// marker index index, i.e. an overlapping attempt committed the turn.
func isMarkerConflict(err error, index string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == index
}
