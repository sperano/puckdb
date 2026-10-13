package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ============================================================================
// simTurnFeed / simDraftPicks — the reads behind `puckdb sim tail` and
// `puckdb sim draft`. Every query is a sqlc query with a named bound; a feed
// page costs at most three SQL queries (turns, their tool calls, the names
// of the player ids in the calls' arguments) and an empty page one.
// ============================================================================

const (
	// simTurnFeedDefaultLimit is the page size without a limit argument; it
	// equals the CLI's tail backlog.
	simTurnFeedDefaultLimit = 10
	// SimTurnFeedMaxLimit is the largest page simTurnFeed serves. The CLI
	// pages with it while following.
	SimTurnFeedMaxLimit = 100
	// simTurnFeedMaxToolCallsPerTurn bounds the tool calls returned per turn;
	// toolCallCount reports the rest.
	simTurnFeedMaxToolCallsPerTurn = 32
	// SimTurnFeedMaxArgumentsBytes bounds each returned arguments preview.
	// Less the up to utf8.UTFMax-1 bytes a rune-boundary cut gives back, it
	// must stay longer than the CLI's preview, so the CLI still truncates
	// (with its "...") from the leading bytes it used to read from the
	// database.
	SimTurnFeedMaxArgumentsBytes = 512
	// simDraftPicksMaxRows bounds simDraftPicks; a pool with more picks is an
	// error rather than a silently cut list.
	simDraftPicksMaxRows = 2000
	// simMinID is the smallest pool id a filter accepts.
	simMinID = 1
)

// simPlayerIDKeys are the tool-argument keys whose integer values are player
// ids.
var simPlayerIDKeys = map[string]bool{"player_id": true, "drop_player_id": true}

// simTurnFeedQuery is a validated simTurnFeed request, narrowed to the
// column types.
type simTurnFeedQuery struct {
	poolID   int32 // 0: every pool
	afterID  int32
	hasAfter bool
	limit    int32
}

// simIntInRange narrows v to int32 after checking it lies in [lo, hi]. hi
// never exceeds math.MaxInt32, so the conversion cannot wrap.
func simIntInRange(name string, v, lo, hi int) (int32, error) {
	if v < lo || v > hi {
		return 0, fmt.Errorf("%s must be between %d and %d, got %d", name, lo, hi, v)
	}
	return int32(v), nil
}

// parseSimTurnFeedArgs rejects out-of-range arguments before any SQL; it
// never clamps a value or swaps it for the default.
func parseSimTurnFeedArgs(poolID, afterID, limit *int) (simTurnFeedQuery, error) {
	q := simTurnFeedQuery{limit: simTurnFeedDefaultLimit}
	var err error
	if poolID != nil {
		if q.poolID, err = simIntInRange("poolId", *poolID, simMinID, math.MaxInt32); err != nil {
			return q, err
		}
	}
	if afterID != nil {
		if q.afterID, err = simIntInRange("afterId", *afterID, 0, math.MaxInt32); err != nil {
			return q, err
		}
		q.hasAfter = true
	}
	if limit != nil {
		if q.limit, err = simIntInRange("limit", *limit, 1, SimTurnFeedMaxLimit); err != nil {
			return q, err
		}
	}
	return q, nil
}

func (r *Resolver) simTurnFeed(ctx context.Context, poolID, afterID, limit *int) (*model.SimTurnFeedPage, error) {
	q, err := parseSimTurnFeedArgs(poolID, afterID, limit)
	if err != nil {
		return nil, err
	}
	turns, hasMore, err := r.listSimTurnFeed(ctx, q)
	if err != nil {
		return nil, err
	}
	page := &model.SimTurnFeedPage{Turns: []*model.SimTurnFeedEntry{}, LastID: int(q.afterID), HasMore: hasMore}
	if len(turns) == 0 {
		return page, nil
	}
	if page.Turns, err = r.assembleSimTurnFeed(ctx, turns); err != nil {
		return nil, err
	}
	page.LastID = int(turns[len(turns)-1].ID)
	return page, nil
}

// listSimTurnFeed reads one page of turns, oldest first. Without afterId it
// is the latest page, which nothing follows. With it, one extra row tells
// whether another page follows.
func (r *Resolver) listSimTurnFeed(ctx context.Context, q simTurnFeedQuery) ([]sqlcdb.ListSimTurnFeedAfterRow, bool, error) {
	if !q.hasAfter {
		latest, err := r.Queries.ListSimTurnFeedLatest(ctx, sqlcdb.ListSimTurnFeedLatestParams{PoolID: q.poolID, RowLimit: q.limit})
		if err != nil {
			return nil, false, fmt.Errorf("list latest sim turns: %w", err)
		}
		turns := make([]sqlcdb.ListSimTurnFeedAfterRow, len(latest))
		for i, t := range latest {
			turns[i] = sqlcdb.ListSimTurnFeedAfterRow(t)
		}
		return turns, false, nil
	}
	turns, err := r.Queries.ListSimTurnFeedAfter(ctx, sqlcdb.ListSimTurnFeedAfterParams{
		AfterID: q.afterID, PoolID: q.poolID, RowLimit: q.limit + 1,
	})
	if err != nil {
		return nil, false, fmt.Errorf("list sim turns after %d: %w", q.afterID, err)
	}
	if len(turns) > int(q.limit) {
		return turns[:q.limit], true, nil
	}
	return turns, false, nil
}

// assembleSimTurnFeed attaches each turn's tool calls and the names of the
// player ids in their arguments: one tool-call query for the page, and one
// name query when any id was found.
func (r *Resolver) assembleSimTurnFeed(ctx context.Context, turns []sqlcdb.ListSimTurnFeedAfterRow) ([]*model.SimTurnFeedEntry, error) {
	ids := make([]int32, len(turns))
	for i, t := range turns {
		ids[i] = t.ID
	}
	calls, err := r.Queries.ListSimToolCallsForTurns(ctx, sqlcdb.ListSimToolCallsForTurnsParams{
		TurnIds: ids, MaxPerTurn: simTurnFeedMaxToolCallsPerTurn,
	})
	if err != nil {
		return nil, fmt.Errorf("list sim tool calls: %w", err)
	}
	playerIDs := make([][]int64, len(calls))
	for i, c := range calls {
		playerIDs[i] = extractSimPlayerIDs(c.ArgumentsRaw)
	}
	names, err := r.simPlayerNames(ctx, slices.Concat(playerIDs...))
	if err != nil {
		return nil, err
	}

	entries := make([]*model.SimTurnFeedEntry, len(turns))
	byTurn := make(map[int32]*model.SimTurnFeedEntry, len(turns))
	for i, t := range turns {
		entries[i] = simTurnFeedEntry(t)
		byTurn[t.ID] = entries[i]
	}
	for i, c := range calls {
		entry := byTurn[c.TurnID]
		entry.ToolCalls = append(entry.ToolCalls, simTurnToolCall(c, simNamesFor(playerIDs[i], names)))
		entry.ToolCallCount = int(c.CallCount)
	}
	return entries, nil
}

// simPlayerNames looks up the display names of ids, distinct, in one query.
// Ids with no player, or whose name is blank, are absent from the map.
func (r *Resolver) simPlayerNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids)
	rows, err := r.Queries.ListPlayerNamesByIDs(ctx, slices.Compact(ids))
	if err != nil {
		return nil, fmt.Errorf("list player names: %w", err)
	}
	names := make(map[int64]string, len(rows))
	for _, p := range rows {
		if name := strings.TrimSpace(p.FirstName + " " + p.LastName); name != "" {
			names[p.ID] = name
		}
	}
	return names, nil
}

// simNamesFor maps ids to names in first-appearance order, dropping
// repeated ids and ids without a name. Never nil: playerNames is non-null.
func simNamesFor(ids []int64, names map[int64]string) []string {
	out := []string{}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if name, ok := names[id]; ok {
			out = append(out, name)
		}
	}
	return out
}

func simTurnFeedEntry(t sqlcdb.ListSimTurnFeedAfterRow) *model.SimTurnFeedEntry {
	return &model.SimTurnFeedEntry{
		ID:              int(t.ID),
		PoolID:          int(t.PoolID),
		AgentID:         int(t.AgentID),
		TeamName:        t.TeamName,
		StrategySummary: t.StrategySummary,
		Model:           t.Model,
		Phase:           t.Phase,
		SimDate:         dateStringPtr(t.SimDate),
		Status:          t.Status,
		ErrorKind:       textPtr(t.ErrorKind),
		Rounds:          int(t.Rounds),
		CostUsd:         t.CostUSD,
		CompletedAt:     t.CompletedAt.Time,
		ToolCalls:       []*model.SimTurnToolCall{},
	}
}

func simTurnToolCall(c sqlcdb.ListSimToolCallsForTurnsRow, names []string) *model.SimTurnToolCall {
	return &model.SimTurnToolCall{
		RoundIndex:           int(c.RoundIndex),
		Sequence:             int(c.Sequence),
		ToolName:             c.ToolName,
		RecoveredName:        textPtr(c.RecoveredName),
		Arguments:            collapseAndCutArguments(c.ArgumentsRaw, SimTurnFeedMaxArgumentsBytes),
		Outcome:              c.Outcome,
		FailureReason:        textPtr(c.FailureReason),
		AppliedTransactionID: int4Ptr(c.AppliedTransactionID),
		PlayerNames:          names,
	}
}

// collapseAndCutArguments collapses whitespace runs to one space, then cuts
// to at most maxBytes on a rune boundary. A cut inside a rune would reach
// the client as U+FFFD (encoding/json replaces invalid UTF-8), changing the
// bytes the CLI prints.
func collapseAndCutArguments(s string, maxBytes int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// extractSimPlayerIDs returns, in first-appearance order of a walk with
// sorted object keys, the integer values of every player_id and
// drop_player_id key at any depth (set_lineup nests them in slots). Only
// integer literals count: 8478402.7 or "8478402" are not ids. Malformed
// JSON, or JSON followed by more data, yields nil.
func extractSimPlayerIDs(argsRaw string) []int64 {
	dec := json.NewDecoder(strings.NewReader(argsRaw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil
	}
	var ids []int64
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(n)) {
				if num, ok := n[k].(json.Number); ok && simPlayerIDKeys[k] {
					if id, err := num.Int64(); err == nil {
						ids = append(ids, id)
					}
				}
				walk(n[k])
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(root)
	return ids
}

// simDraftPicks returns the pool's draft picks in draft order, or nil when
// the pool does not exist. Two queries: the pool, then its picks.
func (r *Resolver) simDraftPicks(ctx context.Context, poolID int) ([]*model.SimDraftPick, error) {
	id, err := simIntInRange("poolId", poolID, simMinID, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	if _, err := r.Queries.GetSimPool(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get sim pool %d: %w", id, err)
	}
	rows, err := r.Queries.ListSimDraftPicks(ctx, sqlcdb.ListSimDraftPicksParams{PoolID: id, RowLimit: simDraftPicksMaxRows + 1})
	if err != nil {
		return nil, fmt.Errorf("list draft picks for pool %d: %w", id, err)
	}
	if len(rows) > simDraftPicksMaxRows {
		return nil, fmt.Errorf("pool %d has more than %d draft picks", id, simDraftPicksMaxRows)
	}
	picks := make([]*model.SimDraftPick, len(rows))
	for i, row := range rows {
		if !row.Round.Valid || !row.Pick.Valid {
			return nil, fmt.Errorf("draft pick of agent %d in pool %d has no round or pick", row.AgentID, id)
		}
		picks[i] = simDraftPick(row)
	}
	return picks, nil
}

func simDraftPick(row sqlcdb.ListSimDraftPicksRow) *model.SimDraftPick {
	return &model.SimDraftPick{
		AgentID:         int(row.AgentID),
		TeamName:        row.TeamName,
		StrategySummary: row.StrategySummary,
		Model:           row.Model,
		DraftPosition:   int4Ptr(row.DraftPosition),
		Round:           int(row.Round.Int32),
		Pick:            int(row.Pick.Int32),
		PlayerID:        row.PlayerID,
		PlayerName:      strings.TrimSpace(row.PlayerName),
		Position:        row.Position,
		Reasoning:       row.Reasoning,
	}
}
