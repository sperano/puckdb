package simulation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/llm/agentloop"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// poolConfigFromRow projects the typed sim_pools row columns onto
// the simulation.PoolConfig struct the workflow uses. Replaces the
// pre-migration `json.Unmarshal(pool.Config, &cfg)` path.
//
// The 8 roster_* columns project onto map[RosterSlot]int by
// listing each slot once. Slots with capacity 0 are kept in the map
// (matches the legacy JSONB shape) — the workflow's roster validators
// don't care; they look up by slot key.
func poolConfigFromRow(p sqlcdb.SimPool) (PoolConfig, error) {
	maxCostUsd, err := NumericToFloat(p.MaxLLMCostUsdPerPool)
	if err != nil {
		return PoolConfig{}, fmt.Errorf("decode max_llm_cost_usd_per_pool: %w", err)
	}
	stop, err := ParseStopAfter(p.StopAfter)
	if err != nil {
		return PoolConfig{}, fmt.Errorf("decode stop_after: %w", err)
	}
	return PoolConfig{
		Season:               int(p.Season),
		NumTeams:             int(p.NumTeams),
		Categories:           append([]string(nil), p.Categories...),
		WaiverDays:           int(p.WaiverDays),
		DraftRounds:          int(p.DraftRounds),
		MaxLLMCostUsdPerPool: maxCostUsd,
		RosterPositions: map[RosterSlot]int{
			SlotC:    int(p.RosterC),
			SlotLW:   int(p.RosterLW),
			SlotRW:   int(p.RosterRW),
			SlotD:    int(p.RosterD),
			SlotG:    int(p.RosterG),
			SlotUtil: int(p.RosterUtil),
			SlotBN:   int(p.RosterBN),
			SlotIR:   int(p.RosterIR),
		},
		StopAfter:     stop,
		MaxSeasonDays: int(p.MaxSeasonDays),
	}, nil
}

// agentConfigFromRow projects the typed sim_agents row columns onto
// simulation.AgentConfig. Replaces the pre-migration
// `json.Unmarshal(a.AgentConfig, &ac)` path. temperature is NULL in
// the DB when the operator wants the provider default; we surface
// that as a nil *float64.
func agentConfigFromRow(a sqlcdb.SimAgent) (AgentConfig, error) {
	cfg := AgentConfig{
		Provider:       a.Provider,
		Model:          a.Model,
		Strategy:       a.Strategy,
		TimeoutSeconds: int(a.TimeoutSeconds),
		APIBase:        a.APIBase,
		MaxTokens:      int(a.MaxTokens),
	}
	if a.Temperature.Valid {
		f, err := NumericToFloat(a.Temperature)
		if err != nil {
			return AgentConfig{}, fmt.Errorf("decode temperature: %w", err)
		}
		cfg.Temperature = &f
	}
	return cfg, nil
}

// ============================================================================
// LoadPoolStateActivity — workflow boot-up.
//
// SimPoolWorkflow calls this once on entry (and once again after each
// ContinueAsNew) to load the static-per-pool config it needs to drive
// the day loop:
//
//   - PoolConfig (decoded from sim_pools.config JSONB)
//   - Per-agent config + IDs (sim_agents rows; AgentConfig decoded
//     from each row's agent_config JSONB)
//   - Season's calendar range (seasons.standings_start /
//     standings_end) so the day loop knows when Phase 2 ends.
//
// Read-only activity, no idempotency concern (rerunning is free).
// ============================================================================

// LoadPoolStateInput is the payload — just the pool ID. Everything
// else the activity needs is keyed off that.
type LoadPoolStateInput struct {
	PoolID int32 `json:"pool_id"`
}

// LoadPoolStateResult bundles the workflow's startup state. AgentIDs
// is parallel to Agents — same length, agentIDs[i] is the
// sim_agents.id for Agents[i] — so the workflow can hand both to
// activities that need them paired (DraftPickActivity needs the ID;
// most context builders need the config).
type LoadPoolStateResult struct {
	PoolConfig PoolConfig    `json:"pool_config"`
	AgentIDs   []int32       `json:"agent_ids"`
	Agents     []AgentConfig `json:"agents"`
	// AgentNotes is parallel to AgentIDs — AgentNotes[i] is the
	// current sim_agents.notes value for Agents[i] at workflow boot.
	// The workflow tracks updates in-memory and refreshes per pick;
	// loading at boot lets a CAN-resumed run start with the most
	// recent notes written by the prior execution.
	AgentNotes []string `json:"agent_notes"`
	// SeasonStartDate / SeasonEndDate are the EFFECTIVE simulation
	// window: the pool's start_date / end_date when set, the season's
	// standings_start / standings_end otherwise (migration 000002).
	// The day loop starts at SeasonStartDate and exits past
	// SeasonEndDate; MaxSeasonDays caps the window on top.
	SeasonStartDate pgtype.Date `json:"season_start_date"`
	SeasonEndDate   pgtype.Date `json:"season_end_date"`
	// PoolStatus and TotalLLMCostUsd seed the workflow's in-memory
	// tracking so QuerySummary can report current values without
	// scheduling an activity call inside the query handler.
	PoolStatus      PoolStatus `json:"pool_status"`
	TotalLLMCostUsd float64    `json:"total_llm_cost_usd"`
}

// LoadPoolState reads sim_pools, sim_agents, and seasons in three
// queries and decodes the JSONB columns into the typed structs the
// workflow uses.
//
// Errors abort the workflow's start (or restart): there's no
// reasonable default for a missing pool config or season range, and
// retrying buys nothing if those rows have actually been deleted.
func (a *Activities) LoadPoolState(ctx context.Context, in LoadPoolStateInput) (LoadPoolStateResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("LoadPoolState start", "pool_id", in.PoolID)

	pool, err := a.Queries.GetSimPool(ctx, in.PoolID)
	if err != nil {
		return LoadPoolStateResult{}, fmt.Errorf("simulation: get sim pool %d: %w", in.PoolID, err)
	}
	cfg, err := poolConfigFromRow(pool)
	if err != nil {
		return LoadPoolStateResult{}, fmt.Errorf("simulation: decode pool config: %w", err)
	}

	agents, err := a.Queries.ListSimAgentsByPool(ctx, in.PoolID)
	if err != nil {
		return LoadPoolStateResult{}, fmt.Errorf("simulation: list agents (pool %d): %w", in.PoolID, err)
	}
	agentIDs := make([]int32, len(agents))
	agentCfgs := make([]AgentConfig, len(agents))
	agentNotes := make([]string, len(agents))
	for i, a := range agents {
		agentIDs[i] = a.ID
		ac, err := agentConfigFromRow(a)
		if err != nil {
			return LoadPoolStateResult{}, fmt.Errorf("simulation: decode agent config (agent %d): %w", a.ID, err)
		}
		agentCfgs[i] = ac
		agentNotes[i] = a.Notes
	}

	// PoolConfig.Season is `int` (matches Go convention); the seasons
	// table key is INT in Postgres → int32 on the Go side.
	season, err := a.Queries.GetSeason(ctx, int32(cfg.Season))
	if err != nil {
		return LoadPoolStateResult{}, fmt.Errorf("simulation: get season %d: %w", cfg.Season, err)
	}

	totalCost, err := NumericToFloat(pool.TotalLLMCostUSD)
	if err != nil {
		return LoadPoolStateResult{}, fmt.Errorf("simulation: decode total_llm_cost_usd: %w", err)
	}

	// Effective window: the pool's own start/end dates override the
	// season bounds when set (createSimPoolImpl validated they lie
	// within the season's standings range).
	windowStart := season.StandingsStart
	if pool.StartDate.Valid {
		windowStart = pool.StartDate
	}
	windowEnd := season.StandingsEnd
	if pool.EndDate.Valid {
		windowEnd = pool.EndDate
	}

	logger.Debug("LoadPoolState complete",
		"agent_count", len(agentIDs),
		"season", cfg.Season,
		"start", windowStart.Time,
		"end", windowEnd.Time,
	)
	return LoadPoolStateResult{
		PoolConfig:      cfg,
		AgentIDs:        agentIDs,
		Agents:          agentCfgs,
		AgentNotes:      agentNotes,
		SeasonStartDate: windowStart,
		SeasonEndDate:   windowEnd,
		PoolStatus:      PoolStatus(pool.Status),
		TotalLLMCostUsd: totalCost,
	}, nil
}

// ============================================================================
// LoadDraftCandidatesActivity — prior-season ranking.
//
// PLAN.md > "Draft Ranking for Available Players": rank candidates
// by PRIOR-season stats only (G+A for skaters, W for goalies).
// "Always prior season, never current" — using current-season stats
// would leak future information into the draft and silently bias
// agent comparison toward whoever happens to perform well in the
// season being replayed.
//
// Implementation:
//   1. Resolve prior season (e.g., 20242025 → 20232024 by walking
//      back one calendar year on each side of the dash).
//   2. Read club_skater_stats / club_goalie_stats for the prior
//      season + game_type='regular_season'. These tables aggregate
//      a player's stats per (player, team), so a traded player has
//      multiple rows that we sum in Go.
//   3. Look up each player's position via one batched
//      GetPlayersByIDs call (was one GetPlayer per player).
//      Skaters with position outside C/LW/RW/D and goalies
//      whose Position isn't G are dropped — V1 doesn't draft "F"
//      players (PLAN.md "5-position table only").
//   4. Skip zero-score players (G+A == 0 for skaters, W == 0 for
//      goalies) — they're below the noise floor and would clog the
//      ~600-row top of the list.
//   5. Return both lists ranked by score descending; the workflow
//      passes them to DraftPickActivity which slices top-K.
// ============================================================================

// LoadDraftCandidatesInput is the payload — just the season number
// the draft is FOR (the activity internally uses prior season for
// "prior-season stats only" per PLAN.md > "Draft Ranking").
type LoadDraftCandidatesInput struct {
	Season int32 `json:"season"`
}

// LoadDraftCandidatesResult is the ranked-candidate output —
// typically ~400-600 skaters and ~50-80 goalies for a full season.
type LoadDraftCandidatesResult struct {
	Skaters []SkaterDraftCandidate `json:"skaters"`
	Goalies []GoalieDraftCandidate `json:"goalies"`
}

// LoadDraftCandidates queries the prior season's club stats,
// aggregates per-player (handling traded players who appear with
// multiple team rows), looks up each player's position, and returns
// the ranked candidate lists.
func (a *Activities) LoadDraftCandidates(ctx context.Context, in LoadDraftCandidatesInput) (LoadDraftCandidatesResult, error) {
	logger := activity.GetLogger(ctx)
	prior, err := priorSeasonID(in.Season)
	if err != nil {
		return LoadDraftCandidatesResult{}, err
	}
	logger.Debug("LoadDraftCandidates", "for_season", in.Season, "prior_season", prior)

	skaters, err := a.loadSkaterCandidates(ctx, prior)
	if err != nil {
		return LoadDraftCandidatesResult{}, err
	}
	goalies, err := a.loadGoalieCandidates(ctx, prior)
	if err != nil {
		return LoadDraftCandidatesResult{}, err
	}

	logger.Debug("LoadDraftCandidates result", "skaters", len(skaters), "goalies", len(goalies))
	return LoadDraftCandidatesResult{
		Skaters: RankSkaters(skaters),
		Goalies: RankGoalies(goalies),
	}, nil
}

// priorSeasonID converts a season ID like 20242025 to its
// predecessor 20232024. The encoding is YYYY1YYYY2 where YYYY1 +
// 1 == YYYY2; "prior season" means each half of the encoded ID
// drops by one.
//
// Errors when EITHER the input or the resulting prior is below
// 19171918 (NHL's first season) — both checks matter because the
// caller wants a usable prior-season ID, not a synthesized one
// pointing at a season that never existed.
func priorSeasonID(season int32) (int32, error) {
	const minSeason = 19171918
	prior := season - 10001
	if season < minSeason || prior < minSeason {
		return 0, fmt.Errorf("season %d has no valid prior NHL season (below minimum %d)", season, minSeason)
	}
	return prior, nil
}

// loadSkaterCandidates aggregates club_skater_stats per (player_id)
// across teams (handles trades), looks up position, filters to the
// 4 valid skater positions, and skips zero-score players.
//
// Position lookups are batched with one GetPlayersByIDs call over
// every surviving (nonzero-score) candidate — previously one GetPlayer
// per candidate, ~400-600 calls for a full season's worth of skaters.
func (a *Activities) loadSkaterCandidates(ctx context.Context, season int32) ([]SkaterDraftCandidate, error) {
	rows, err := a.Queries.GetClubSkaterStatsBySeason(ctx, sqlcdb.GetClubSkaterStatsBySeasonParams{
		Season:   season,
		GameType: sqlcdb.GameTypeRegularSeason,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: get prior-season skater stats: %w", err)
	}

	type agg struct {
		first, last string
		goals       int
		assists     int
	}
	byPlayer := map[int64]*agg{}
	for _, r := range rows {
		entry, ok := byPlayer[r.PlayerID]
		if !ok {
			entry = &agg{first: r.FirstName, last: r.LastName}
			byPlayer[r.PlayerID] = entry
		}
		entry.goals += int(r.Goals)
		entry.assists += int(r.Assists)
	}

	// Drop zero-score players before the batch lookup — they're
	// filtered regardless, so there's no reason to fetch their
	// position too.
	ids := make([]int64, 0, len(byPlayer))
	for id, cand := range byPlayer {
		if cand.goals+cand.assists == 0 {
			continue // below the noise floor — no draft signal
		}
		ids = append(ids, id)
	}
	players, err := loadPlayersByIDs(ctx, a.Queries, ids)
	if err != nil {
		return nil, fmt.Errorf("simulation: batch load skater candidate players: %w", err)
	}

	out := make([]SkaterDraftCandidate, 0, len(ids))
	for _, id := range ids {
		cand := byPlayer[id]
		p, found := players[id]
		if !found {
			continue // club stats row with no matching players row (data gap)
		}
		pos, ok := skaterDraftPosition(p)
		if !ok {
			continue // F / G / unknown → not a skater draft candidate
		}
		out = append(out, SkaterDraftCandidate{
			PlayerID: id,
			Name:     cand.first + " " + cand.last,
			Position: pos,
			PriorG:   cand.goals,
			PriorA:   cand.assists,
		})
	}
	return out, nil
}

// loadGoalieCandidates is the goalie-side of the candidate dance.
// Sums Wins + GoalsAgainst across team rows; filters position to G.
// Position lookups batch the same way loadSkaterCandidates does.
func (a *Activities) loadGoalieCandidates(ctx context.Context, season int32) ([]GoalieDraftCandidate, error) {
	rows, err := a.Queries.GetClubGoalieStatsBySeason(ctx, sqlcdb.GetClubGoalieStatsBySeasonParams{
		Season:   season,
		GameType: sqlcdb.GameTypeRegularSeason,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: get prior-season goalie stats: %w", err)
	}

	type agg struct {
		first, last string
		wins        int
		ga          int
	}
	byPlayer := map[int64]*agg{}
	for _, r := range rows {
		entry, ok := byPlayer[r.PlayerID]
		if !ok {
			entry = &agg{first: r.FirstName, last: r.LastName}
			byPlayer[r.PlayerID] = entry
		}
		entry.wins += int(r.Wins)
		entry.ga += int(r.GoalsAgainst)
	}

	ids := make([]int64, 0, len(byPlayer))
	for id, cand := range byPlayer {
		if cand.wins == 0 {
			continue // a goalie with zero W has no draft signal
		}
		ids = append(ids, id)
	}
	players, err := loadPlayersByIDs(ctx, a.Queries, ids)
	if err != nil {
		return nil, fmt.Errorf("simulation: batch load goalie candidate players: %w", err)
	}

	out := make([]GoalieDraftCandidate, 0, len(ids))
	for _, id := range ids {
		cand := byPlayer[id]
		p, found := players[id]
		if !found {
			continue // club stats row with no matching players row (data gap)
		}
		// Goalies share the position-lookup helper — skaterDraftPosition
		// returns ok=false for G, so we use a goalie-specific path.
		if !isGoalie(p) {
			continue
		}
		out = append(out, GoalieDraftCandidate{
			PlayerID: id,
			Name:     cand.first + " " + cand.last,
			Position: "G",
			PriorW:   cand.wins,
			PriorGA:  cand.ga,
		})
	}
	return out, nil
}

// skaterDraftPosition returns the prompt-side position string
// ("C"/"LW"/"RW"/"D") for an already-loaded player, or ok=false for
// any other value (including F, G, NULL, unknown).
func skaterDraftPosition(p sqlcdb.Player) (string, bool) {
	if !p.Position.Valid {
		return "", false
	}
	switch p.Position.PlayerPosition {
	case sqlcdb.PlayerPositionC:
		return "C", true
	case sqlcdb.PlayerPositionLW:
		return "LW", true
	case sqlcdb.PlayerPositionRW:
		return "RW", true
	case sqlcdb.PlayerPositionD:
		return "D", true
	default:
		return "", false
	}
}

// isGoalie returns true iff the player's position is G.
func isGoalie(p sqlcdb.Player) bool {
	return p.Position.Valid && p.Position.PlayerPosition == sqlcdb.PlayerPositionG
}

// ============================================================================
// SetPoolStatusActivity — workflow status writes.
//
// SimPoolWorkflow can't issue DB writes directly (workflow goroutines
// must be deterministic; DB calls are non-deterministic side
// effects), so status flips (after draft, on cancel, on completion)
// go through this tiny activity.
//
// The activity is a 1-call wrapper around UpdateSimPoolStatus. Kept
// separate from the bigger CRUD activities so the workflow's
// completePool / cancel-handling paths can reach it without dragging
// in the LLM-activities boot sequence.
// ============================================================================

// SetPoolStatusInput is the per-call payload.
type SetPoolStatusInput struct {
	PoolID int32      `json:"pool_id"`
	Status PoolStatus `json:"status"`
}

// RecordDraftOrderInput carries the shuffled round-1 agent order
// to persist into sim_agents.draft_position. agent_ids[i] gets
// draft_position = i+1.
type RecordDraftOrderInput struct {
	AgentIDs []int32 `json:"agent_ids"`
}

// RecordDraftOrder persists the workflow's shuffled draft order to
// sim_agents.draft_position. Idempotent — re-runs overwrite with the
// same values when called with the same input (which is what
// SideEffect determinism guarantees within one execution).
func (a *Activities) RecordDraftOrder(ctx context.Context, in RecordDraftOrderInput) error {
	for i, agentID := range in.AgentIDs {
		if err := a.Queries.SetSimAgentDraftPosition(ctx, sqlcdb.SetSimAgentDraftPositionParams{
			ID:            agentID,
			DraftPosition: pgtype.Int4{Int32: int32(i + 1), Valid: true},
		}); err != nil {
			return fmt.Errorf("simulation: set draft_position for agent %d: %w", agentID, err)
		}
	}
	return nil
}

// PickTeamNameInput carries everything PickTeamName needs to invoke
// an LLM once and persist the chosen name.
type PickTeamNameInput struct {
	PoolID      int32       `json:"pool_id"`
	AgentID     int32       `json:"agent_id"`
	AgentConfig AgentConfig `json:"agent_config"`
}

// PickTeamNameResult carries the chosen name + cost so the workflow
// can aggregate spend and surface it in metrics.
type PickTeamNameResult struct {
	Name    string  `json:"name"`
	CostUsd float64 `json:"cost_usd"`
}

// MaxTeamNameToolRounds caps the team-name agentloop. 2 leaves a one-shot
// retry window so weaker models that emit prose without calling the tool
// on the first round get one chance to correct themselves before the
// activity gives up.
const MaxTeamNameToolRounds = 2

// MaxStrategySummaryChars caps the agent-supplied summary at 30 chars
// so the tail's "team_name (summary, model)" line stays compact even
// with verbose models. Matches the setTeamNameSchema.summary maxLength.
const MaxStrategySummaryChars = 30

// PickTeamName runs an agentloop turn asking the agent to commit a team
// name via the set_team_name tool, persists the chosen name, and writes
// the full telemetry trace (sim_agent_turns + children) in the same tx.
//
// Idempotent: RecordTurnTelemetry deletes any prior sim_agent_turns row
// at the same (pool, agent, phase=team_name) coordinate inside the
// same tx before inserting the new one, so a Temporal retry (or a
// manual re-invocation) replaces the previous attempt's row instead of
// colliding with the partial unique index ux_sim_agent_turns_no_date.
// ON DELETE CASCADE removes the prior turn's rounds / tool_calls /
// messages so child PKs don't collide either.
func (a *Activities) PickTeamName(ctx context.Context, in PickTeamNameInput) (PickTeamNameResult, error) {
	logger := activity.GetLogger(ctx)

	agent, err := a.getOrCreateAgent(in.PoolID, in.AgentID, in.AgentConfig, 0)
	if err != nil {
		return PickTeamNameResult{}, fmt.Errorf("simulation: build agent for team-name pick: %w", err)
	}

	startedAt := time.Now()

	userMsg := fmt.Sprintf(
		"You are about to start a fantasy hockey simulation. "+
			"Your strategy: %s\n\n"+
			"Call the set_team_name tool exactly once with TWO arguments:\n"+
			"  - name: a memorable team name (1-50 chars) reflecting your strategy or persona\n"+
			"  - summary: a very terse strategy label (<= 30 chars) for display in dashboards",
		in.AgentConfig.Strategy,
	)
	messages := []llm.Message{{Role: "user", Content: userMsg}}

	// Per-turn latency recorder + tool-call accumulator. The closure
	// captures these by reference; agentloop.Run will call back into
	// the executor for each tool the LLM emits.
	recorder := newLatencyRecordingClient(agent.Client)
	var toolCaptures []ToolCallCapture
	var chosenName, chosenSummary string
	exec := func(_ context.Context, call llm.ToolCall) (string, error) {
		callStart := time.Now()
		capture := ToolCallCapture{
			ToolName:     call.Function.Name,
			ArgumentsRaw: call.Function.Arguments,
		}

		action, dist, recErr := RecoverAction(call, TeamNameTools())
		if recErr != nil {
			capture.Outcome = ToolCallOutcomeParseError
			capture.FailureReason = recErr.Error()
			capture.Result = fmt.Sprintf("error: %v", recErr)
			capture.Latency = time.Since(callStart)
			toolCaptures = append(toolCaptures, capture)
			return capture.Result, nil
		}
		capture.RecoveredName = RecoveredName(call.Function.Name, action, dist)

		args, ok := action.(SetTeamNameArgs)
		if !ok {
			capture.Outcome = ToolCallOutcomeUnhandled
			capture.FailureReason = fmt.Sprintf("expected set_team_name, got %s", action.ToolName())
			capture.Result = fmt.Sprintf("error: %s", capture.FailureReason)
			capture.Latency = time.Since(callStart)
			toolCaptures = append(toolCaptures, capture)
			return capture.Result, nil
		}

		name := strings.TrimSpace(args.Name)
		summary := strings.TrimSpace(args.Summary)
		if name == "" {
			capture.Outcome = ToolCallOutcomeValidationRejected
			capture.FailureReason = "team name is empty after trim"
			capture.Result = "error: team name is empty after trim"
			capture.Latency = time.Since(callStart)
			toolCaptures = append(toolCaptures, capture)
			return capture.Result, nil
		}
		if summary == "" {
			capture.Outcome = ToolCallOutcomeValidationRejected
			capture.FailureReason = "summary is empty after trim"
			capture.Result = "error: summary is required (very terse strategy label, <= 30 chars)"
			capture.Latency = time.Since(callStart)
			toolCaptures = append(toolCaptures, capture)
			return capture.Result, nil
		}
		if len(summary) > MaxStrategySummaryChars {
			capture.Outcome = ToolCallOutcomeValidationRejected
			capture.FailureReason = fmt.Sprintf("summary too long (%d chars, max %d)", len(summary), MaxStrategySummaryChars)
			capture.Result = fmt.Sprintf("error: summary must be <= %d chars", MaxStrategySummaryChars)
			capture.Latency = time.Since(callStart)
			toolCaptures = append(toolCaptures, capture)
			return capture.Result, nil
		}

		chosenName = name
		chosenSummary = summary
		capture.Outcome = ToolCallOutcomeAccepted
		capture.Result = "ok: team name set"
		capture.Latency = time.Since(callStart)
		toolCaptures = append(toolCaptures, capture)
		return capture.Result, nil
	}

	res, runErr := agentloop.Run(ctx, recorder, messages, exec, agentloop.Config{
		Tools:         TeamNameTools(),
		MaxToolRounds: MaxTeamNameToolRounds,
		MaxTokens:     in.AgentConfig.MaxTokens,
		Temperature:   in.AgentConfig.Temperature,
	})
	completedAt := time.Now()

	costUsd := costFromAggregate(in.AgentConfig, res)

	// Build the telemetry header up front so even the error paths
	// persist what they have. The persisted row tells operators
	// "this agent's team-name run failed; here's the cost and the
	// rejected tool calls" rather than silently swallowing the attempt.
	header := TurnHeader{
		PoolID:      in.PoolID,
		AgentID:     in.AgentID,
		Phase:       TurnPhaseTeamName,
		Provider:    in.AgentConfig.Provider,
		Model:       in.AgentConfig.Model,
		Temperature: in.AgentConfig.Temperature,
		MaxTokens:   in.AgentConfig.MaxTokens,
		CostUsd:     costUsd,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}

	if res != nil {
		AssignRoundsFromAudit(toolCaptures, res.Audit)
		if res.Final != nil {
			header.FinalText = res.Final.Content
		}
	}

	if runErr != nil {
		header.Status = TurnStatusErrored
		header.ErrorKind = ErrorKindLLMError
		header.ErrorDetail = runErr.Error()
		if err := a.persistTeamNameTurn(ctx, in.PoolID, in.AgentID, "", "", header, recorder.Captures(), res, toolCaptures); err != nil {
			return PickTeamNameResult{}, err
		}
		return PickTeamNameResult{CostUsd: costUsd}, fmt.Errorf("simulation: team-name agent loop: %w", runErr)
	}

	if chosenName == "" {
		// The loop terminated without a successful set_team_name call.
		// Persist the turn as errored so the operator can inspect the
		// rejected calls + final_text.
		header.Status = TurnStatusErrored
		header.ErrorKind = ErrorKindToolUseFailure
		header.ErrorDetail = "no successful set_team_name call"
		if err := a.persistTeamNameTurn(ctx, in.PoolID, in.AgentID, "", "", header, recorder.Captures(), res, toolCaptures); err != nil {
			return PickTeamNameResult{}, err
		}
		return PickTeamNameResult{CostUsd: costUsd}, fmt.Errorf("simulation: team-name response produced no valid name")
	}

	header.Status = TurnStatusOK
	if err := a.persistTeamNameTurn(ctx, in.PoolID, in.AgentID, chosenName, chosenSummary, header, recorder.Captures(), res, toolCaptures); err != nil {
		return PickTeamNameResult{}, err
	}

	logger.Debug("PickTeamName complete",
		"pool_id", in.PoolID,
		"agent_id", in.AgentID,
		"name", chosenName,
		"cost_usd", costUsd,
		"rounds", len(recorder.Captures()),
	)
	return PickTeamNameResult{Name: chosenName, CostUsd: costUsd}, nil
}

// persistTeamNameTurn writes the team-name UPDATE (when name is non-empty)
// AND the full telemetry trace in one atomic-commit block. Wrapping both
// in InTx keeps the two writes either both visible or both rolled back —
// otherwise a crash between them would leave sim_agents.team_name set
// without a corresponding telemetry row (or vice versa).
func (a *Activities) persistTeamNameTurn(
	ctx context.Context,
	poolID, agentID int32,
	name, summary string,
	header TurnHeader,
	captures []roundCapture,
	res *agentloop.Result,
	toolCaptures []ToolCallCapture,
) error {
	recordMessages, err := a.Queries.GetSimPoolRecordFullMessages(ctx, poolID)
	if err != nil {
		return fmt.Errorf("simulation: fetch record_full_messages flag: %w", err)
	}
	var messages []llm.Message
	if recordMessages && res != nil {
		messages = res.Messages
	}

	return a.Tx.InTx(ctx, func(q SimQueries) error {
		if name != "" {
			if err := q.SetSimAgentTeamNameAndSummary(ctx, sqlcdb.SetSimAgentTeamNameAndSummaryParams{
				ID:              agentID,
				TeamName:        name,
				StrategySummary: summary,
			}); err != nil {
				return fmt.Errorf("persist team_name+summary for agent %d: %w", agentID, err)
			}
		}
		if _, err := RecordTurnTelemetry(ctx, q, TurnTelemetry{
			Header:         header,
			Captures:       captures,
			ToolCalls:      toolCaptures,
			Messages:       messages,
			RecordMessages: recordMessages,
		}); err != nil {
			return err
		}
		return nil
	})
}

// SetPoolStatus writes sim_pools.status. Idempotent (the same call
// twice produces the same row state); errors propagate so a flaky DB
// connection lets Temporal retry.
func (a *Activities) SetPoolStatus(ctx context.Context, in SetPoolStatusInput) error {
	activity.GetLogger(ctx).Debug("SetPoolStatus", "pool_id", in.PoolID, "status", in.Status)
	if err := a.Queries.UpdateSimPoolStatus(ctx, sqlcdb.UpdateSimPoolStatusParams{
		ID:     in.PoolID,
		Status: string(in.Status),
	}); err != nil {
		return fmt.Errorf("simulation: update pool status to %s: %w", in.Status, err)
	}
	return nil
}
