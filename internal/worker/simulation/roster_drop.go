package simulation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// errDropVanished reports that a player the caller saw on the roster
// was gone when its delete ran. Every caller validated the player as
// rostered first (the turn's working state, or the waiver plan's read
// inside the same transaction), so this is a broken invariant that
// fails the transaction rather than a tolerated case.
var errDropVanished = errors.New("drop player vanished from roster mid-transaction")

// rosterDrop names one player leaving one agent's roster.
type rosterDrop struct {
	PoolID    int32
	AgentID   int32
	Date      pgtype.Date
	PlayerID  int64
	Reasoning string
}

// removeAndLogDrop is the single write path that takes a player off a
// roster: drop_player, add_player's drop_player_id replacement and the
// drop of a won waiver claim all go through it. In the caller's
// transaction it deletes the roster row and logs a type='drop'
// sim_transactions row, returning that row's ID.
//
// The drop row is the player's waiver marker: ListSimPlayersOnWaivers
// and ListSimFreeAgentCandidates find waiver windows only from
// type='drop' rows, so a removal without one would make the player a
// free agent at once. A replacement's add row keeps drop_player_id as
// the add/drop relation for history; it is not a second marker.
//
// A delete that affects no row fails with errDropVanished instead of
// logging a drop (and a waiver window) for a player nobody removed.
func removeAndLogDrop(ctx context.Context, q SimQueries, d rosterDrop) (int32, error) {
	affected, err := q.DeleteSimRosterRows(ctx, sqlcdb.DeleteSimRosterRowsParams{
		PoolID: d.PoolID, AgentID: d.AgentID, PlayerID: d.PlayerID,
	})
	if err != nil {
		return 0, fmt.Errorf("delete roster row (agent %d, player %d): %w", d.AgentID, d.PlayerID, err)
	}
	if affected == 0 {
		return 0, fmt.Errorf("agent %d, player %d: %w", d.AgentID, d.PlayerID, errDropVanished)
	}
	tx, err := q.InsertSimTransactionDrop(ctx, sqlcdb.InsertSimTransactionDropParams{
		PoolID:    d.PoolID,
		AgentID:   d.AgentID,
		Date:      d.Date,
		PlayerID:  pgtype.Int8{Int64: d.PlayerID, Valid: true},
		Reasoning: d.Reasoning,
	})
	if err != nil {
		return 0, fmt.Errorf("insert drop tx (agent %d, player %d): %w", d.AgentID, d.PlayerID, err)
	}
	return tx.ID, nil
}
