package maurice

import (
	"bytes"
	"time"

	"github.com/sperano/puckdb/internal/llm"
)

// Backend-neutral pieces of the turn contract shared by the PostgreSQL and
// SQLite stores.

// storedTurn is a turn a backend found under an idempotency key.
type storedTurn struct {
	ID             string
	ConversationID string
	RequestHash    []byte
	Status         TurnStatus
	ErrorClass     ErrorClass
	// Stale reports a running turn older than BeginTurnParams.StaleAfter.
	Stale bool
	// ConversationDeleted reports that the owner deleted the turn's
	// conversation.
	ConversationDeleted bool
	Warnings            []string
}

// checkKeyReuse decides what a reused idempotency key means. It rejects a
// key whose conversation was deleted, a different prompt or conversation, and
// a turn still running. abandon reports a stale running turn that the caller
// must fail as abandoned before replaying it.
func checkKeyReuse(params BeginTurnParams, turn storedTurn) (abandon bool, err error) {
	if turn.ConversationDeleted {
		return false, ErrConversationNotFound
	}
	if !bytes.Equal(turn.RequestHash, params.RequestHash) {
		return false, ErrIdempotencyKeyReused
	}
	if params.ConversationID != "" && params.ConversationID != turn.ConversationID {
		return false, ErrIdempotencyKeyReused
	}
	if turn.Status != TurnRunning {
		return false, nil
	}
	if !turn.Stale {
		return false, ErrTurnInProgress
	}
	return true, nil
}

// abandonedReplay is the replay of a turn just failed as abandoned.
func abandonedReplay(turn storedTurn) *TurnStart {
	return &TurnStart{
		TurnID:         turn.ID,
		ConversationID: turn.ConversationID,
		Replay:         &TurnReplay{Status: TurnFailed, ErrorClass: ErrorClassAbandoned},
	}
}

// staleCutoffSeconds converts StaleAfter for SQL interval arithmetic.
func staleCutoffSeconds(params BeginTurnParams) float64 {
	return params.StaleAfter.Seconds()
}

// toolNames lists the tool names requested by a turn's assistant messages,
// in order. Every requested call of a succeeded turn was executed, so this is
// the turn's ToolsUsed.
func toolNames(lists [][]llm.ToolCall) []string {
	var names []string
	for _, calls := range lists {
		for _, c := range calls {
			names = append(names, c.Function.Name)
		}
	}
	return names
}

// createdAtStep is the spacing between successive messages' created_at values
// within a turn. Both stores keep microsecond-resolution timestamps, so a full
// microsecond keeps them distinct and strictly increasing in turn order.
const createdAtStep = time.Microsecond
