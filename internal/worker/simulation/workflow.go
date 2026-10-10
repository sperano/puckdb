package simulation

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ============================================================================
// SimPoolWorkflow — orchestrator for one simulation pool's lifetime.
//
// Phases:
//
//	1. Draft: snake-order draft of every roster spot. Skipped on
//	   ContinueAsNew resume (input.SimDate is already set).
//	2. Season: day loop. Each iteration processes one CALENDAR day:
//	     ProcessWaivers → BuildFreeAgentPool → ManageRoster (per agent)
//	     → if FINAL games: CollectDayStats → UpdateStandings
//	   The loop is signal-driven: each `advance` signal grants one
//	   day; `auto_advance` runs continuously; `pause` blocks both.
//	   Every 30 days the workflow returns ContinueAsNewError to keep
//	   the Temporal event history bounded.
//	3. Complete: when sim_date passes the season's standings_end,
//	   sim_pools.status flips to 'complete' and the workflow exits.
//
// Determinism discipline (PLAN.md > "Determinism"):
//   - All randomness threads through workflow.SideEffect to capture
//     a seed once at the relevant decision point; math/rand on top of
//     that seed is replay-safe (the seed itself is recorded in event
//     history). PLAN.md mentions a workflow.NewRandom helper — that
//     API doesn't exist in the SDK; SideEffect is the canonical
//     primitive.
//   - All wall-clock reads via workflow.Now(ctx). NEVER time.Now().
//   - All side-effecting reads/writes go through activities.
//   - Maps are NOT iterated for ordered work — the iteration order
//     would replay non-deterministically.
//
// Cancellation: workflow.Context's Done channel fires when the
// caller invokes RequestCancelWorkflow. We honor it by writing
// status='cancelled' and returning the cancel error so Temporal
// records the workflow as Cancelled (not Failed).
// ============================================================================

// Query names. Each handler is registered via workflow.SetQueryHandler.
const (
	QuerySummary          = "summary"
	QueryLatestStandings  = "latest_standings"
	QueryStandingsForDate = "standings_for_date"
	QueryRosters          = "rosters"
	QueryLog              = "log"
)

// ContinueAsNewDayThreshold is the calendar-day count at which the
// workflow rolls over via ContinueAsNew. PLAN.md > "ContinueAsNew
// every 30 days" — keeps Temporal's event history bounded.
const ContinueAsNewDayThreshold = 30

// hoursPerDay converts an elapsed duration to whole calendar days for
// the season's day arithmetic. All sim dates are midnight-aligned, so
// integer division by 24 hours yields the day delta exactly.
const hoursPerDay = 24

// SimPoolWorkflowInput is the per-execution payload. Initial start
// (from createSimPool mutation): SimDate is unset (the workflow loads
// season start), DayCount=0. ContinueAsNew carries forward SimDate
// only; DayCount resets to 0 so the CAN threshold counter restarts.
type SimPoolWorkflowInput struct {
	PoolID   int32       `json:"pool_id"`
	SimDate  pgtype.Date `json:"sim_date"`
	DayCount int         `json:"day_count"`
}

// PoolSummary is the polled-often query payload. Fixed-size, cheap
// to serialize, exposes the user-visible state of the pool.
type PoolSummary struct {
	Status          string      `json:"status"`
	SimDate         pgtype.Date `json:"sim_date"`
	DayCount        int         `json:"day_count"`
	TotalLLMCostUsd float64     `json:"total_llm_cost_usd"`
}

// SimPoolWorkflow is the top-level workflow function.
//
// Failure modes:
//   - Unrecoverable activity errors abort the workflow (Temporal's
//     activity retry policy handles transient failures; only after
//     exhaustion does the error reach here).
//   - workflow.Context.Done fires on RequestCancelWorkflow → we
//     write status='cancelled' and return ctx.Err().
//   - Otherwise: regular-season end → write status='complete' and
//     return nil.
func SimPoolWorkflow(ctx workflow.Context, in SimPoolWorkflowInput) (retErr error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("SimPoolWorkflow start",
		"pool_id", in.PoolID,
		"sim_date", in.SimDate.Time,
		"day_count", in.DayCount,
	)

	// Single cancel-cleanup hook: any phase that returns because ctx
	// is cancelled flips the pool row to 'cancelled' via a
	// disconnected context (the workflow's own ctx is already
	// cancelled and would short-circuit ExecuteActivity).
	defer func() {
		if shouldMarkCancelled(retErr) {
			cleanupCtx, cancel := workflow.NewDisconnectedContext(ctx)
			defer cancel()
			if err := writePoolStatus(cleanupCtx, in.PoolID, PoolStatusCancelled); err != nil {
				logger.Warn("Failed to write status=cancelled on cancel", "err", err)
			}
		}
	}()

	state, err := loadSimState(ctx, in)
	if err != nil {
		return fmt.Errorf("load pool state: %w", err)
	}

	tracker, err := setupTracker(ctx, in, state)
	if err != nil {
		return fmt.Errorf("setup progress tracker: %w", err)
	}

	if err := registerQueryHandlers(ctx, &state, &in); err != nil {
		return fmt.Errorf("register query handlers: %w", err)
	}

	// Cost-cap pause: when the cost-cap activity flips the pool to
	// 'paused' in the DB, it also sends this signal so the workflow
	// can exit cleanly between activities. Checked via ReceiveAsync
	// at phase / day boundaries — no Selector, no blocking wait.
	pauseCh := workflow.GetSignalChannel(ctx, SignalPause)
	checkPause := func() bool {
		// ReceiveAsync returns true iff a signal is queued. On true
		// we don't need the payload (it's nil from costcap.go).
		return pauseCh.ReceiveAsync(nil)
	}

	// Phase 0: each agent picks its team name. Runs once per workflow
	// execution (skipped on CAN resume since SimDate.Valid means we're
	// past the initial start). Activities fan out per-agent in parallel.
	if !in.SimDate.Valid {
		if err := runTeamNamePhase(ctx, &state); err != nil {
			return fmt.Errorf("team-name phase: %w", err)
		}
		if checkPause() {
			logger.Info("SimPoolWorkflow exit (cost cap reached after team-name)", "pool_id", in.PoolID)
			return nil
		}
		if state.PoolConfig.StopAfter == StopAfterTeamName {
			logger.Info("SimPoolWorkflow exit (StopAfterTeamName)", "pool_id", in.PoolID)
			state.CurrentStatus = PoolStatusComplete
			return completePoolStatusOnly(ctx, in.PoolID)
		}
	}

	// Phase 1: Draft. Skipped on CAN resume.
	if !in.SimDate.Valid {
		state.CurrentStatus = PoolStatusDraft
		if err := runDraftPhase(ctx, &state, tracker); err != nil {
			return fmt.Errorf("draft phase: %w", err)
		}
		if checkPause() {
			state.CurrentStatus = PoolStatusPaused
			logger.Info("SimPoolWorkflow exit (cost cap reached after draft)", "pool_id", in.PoolID)
			return nil
		}
		if state.PoolConfig.StopAfter == StopAfterDraft {
			logger.Info("SimPoolWorkflow exit (StopAfterDraft)", "pool_id", in.PoolID)
			state.CurrentStatus = PoolStatusComplete
			return completePoolStatusOnly(ctx, in.PoolID)
		}
		in.SimDate = state.SeasonStartDate
	}

	state.CurrentStatus = PoolStatusRunning
	// Phase 2: Season day loop.
	paused, err := runSeasonPhase(ctx, &in, &state, tracker, checkPause)
	if err != nil {
		// ContinueAsNew is signaled via a sentinel error from
		// runSeasonPhase — propagate as-is so Temporal recognizes it.
		return err
	}
	if paused {
		// Cost-cap pause mid-season: the cost-cap activity already
		// flipped the pool to 'paused'. Do NOT fall through to
		// completePool, which would overwrite that status with
		// 'complete'.
		state.CurrentStatus = PoolStatusPaused
		logger.Info("SimPoolWorkflow exit (cost cap reached during season)", "pool_id", in.PoolID)
		return nil
	}

	// Phase 3: Complete.
	state.CurrentStatus = PoolStatusComplete
	if err := completePool(ctx, in.PoolID, tracker); err != nil {
		return fmt.Errorf("complete phase: %w", err)
	}
	logger.Info("SimPoolWorkflow complete", "pool_id", in.PoolID)
	return nil
}

// shouldMarkCancelled reports whether a workflow that returned retErr
// ended because of a genuine cancellation, as opposed to a
// ContinueAsNew rollover or a normal completion. This gates the
// cancel-cleanup defer's status='cancelled' write.
//
// retErr also carries workflow.NewContinueAsNewError on the 30-day
// rollover; a cancel arriving in the same decision window as a CAN
// would otherwise (under a bare `ctx.Err() != nil` check) mark a
// still-continuing pool cancelled. We exclude CAN explicitly and
// require a real CanceledError so only an actual cancellation counts.
func shouldMarkCancelled(retErr error) bool {
	if retErr == nil {
		return false
	}
	if workflow.IsContinueAsNewError(retErr) {
		return false
	}
	return temporal.IsCanceledError(retErr)
}

// simState is the workflow's view of the pool. Built once from
// LoadPoolStateActivity; immutable across the workflow's lifetime
// EXCEPT for CurrentStatus and TotalLLMCostUsd, which are updated
// in-place as the workflow transitions through phases and the LLM
// activities report costs. These mutable fields are closed over by
// the QuerySummary handler so it can answer without scheduling work.
type simState struct {
	PoolID     int32
	PoolConfig PoolConfig
	AgentIDs   []int32
	Agents     []AgentConfig
	// AgentConfigByID indexes Agents by agent ID — built once in
	// loadSimState from the parallel AgentIDs/Agents slices so the
	// draft and daily-turn loops can resolve an AgentConfig with an
	// O(1) lookup instead of an O(agents) scan per pick/turn.
	AgentConfigByID map[int32]AgentConfig
	// AgentNotes is parallel to AgentIDs — initial sim_agents.notes
	// values at workflow boot. The runDraftPhase loop maintains its
	// own mutable notesByAgent map keyed by agentID; the entries here
	// just seed it.
	AgentNotes      []string
	SeasonStartDate pgtype.Date
	SeasonEndDate   pgtype.Date
	WorkflowID      string
	TotalDays       int
	// CurrentStatus mirrors sim_pools.status for QuerySummary.
	// Updated whenever the workflow writes a new status to the DB.
	CurrentStatus PoolStatus
	// TotalLLMCostUsd mirrors sim_pools.total_llm_cost_usd. Seeded
	// at workflow start from the DB row; updated by each activity
	// result that reports a cost. This is an approximation — it may
	// lag the DB by one activity completion if a worker crashes
	// between the activity commit and the workflow updating this
	// field. That's acceptable for the query path; cost-cap logic
	// still reads the DB directly.
	TotalLLMCostUsd float64
}

// loadSimState calls LoadPoolStateActivity and computes derived
// quantities (TotalDays, WorkflowID).
func loadSimState(ctx workflow.Context, in SimPoolWorkflowInput) (simState, error) {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	var acts *Activities
	var res LoadPoolStateResult
	if err := workflow.ExecuteActivity(ctx, acts.LoadPoolState, LoadPoolStateInput{PoolID: in.PoolID}).Get(ctx, &res); err != nil {
		return simState{}, err
	}
	totalDays := 0
	if res.SeasonStartDate.Valid && res.SeasonEndDate.Valid {
		// Inclusive of both endpoints — every calendar day in the
		// regular season gets one iteration of the day loop.
		days := int(res.SeasonEndDate.Time.Sub(res.SeasonStartDate.Time).Hours()/hoursPerDay) + 1
		if days > 0 {
			totalDays = days
		}
	}
	agentConfigByID := make(map[int32]AgentConfig, len(res.AgentIDs))
	for i, id := range res.AgentIDs {
		agentConfigByID[id] = res.Agents[i]
	}
	return simState{
		PoolID:          in.PoolID,
		PoolConfig:      res.PoolConfig,
		AgentIDs:        res.AgentIDs,
		Agents:          res.Agents,
		AgentConfigByID: agentConfigByID,
		AgentNotes:      res.AgentNotes,
		SeasonStartDate: res.SeasonStartDate,
		SeasonEndDate:   res.SeasonEndDate,
		WorkflowID:      workflow.GetInfo(ctx).WorkflowExecution.ID,
		TotalDays:       totalDays,
		CurrentStatus:   res.PoolStatus,
		TotalLLMCostUsd: res.TotalLLMCostUsd,
	}, nil
}

// setupTracker initializes (or rehydrates from Redis on CAN) the
// progress tracker. Two groups:
//   - Group 0 "Draft": Total = num_teams × draft_rounds.
//   - Group 1 "Season": Total = total calendar days.
func setupTracker(ctx workflow.Context, in SimPoolWorkflowInput, state simState) (*shared.ReportTracker, error) {
	if !in.SimDate.Valid && in.DayCount == 0 {
		// First run — fresh tracker.
		return shared.InitTracker(ctx, NewProgressReport(state))
	}
	// CAN resume — rehydrate from Redis. Falls back to a fresh
	// tracker if Redis is empty (e.g., the first CAN of a workflow
	// that pre-dates the tracker's introduction).
	tracker, err := shared.LoadReportTracker(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("LoadReportTracker failed, falling back to fresh tracker", "err", err)
		return shared.InitTracker(ctx, NewProgressReport(state))
	}
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	return tracker, nil
}

// NewProgressReport builds the two-group progress report for a sim
// pool. Exported so cmd/worker.go and tests can construct identical
// reports.
func NewProgressReport(state simState) *shared.ProgressReport {
	draftTotal := state.PoolConfig.NumTeams * state.PoolConfig.DraftRounds
	seasonTotal := state.TotalDays
	total := draftTotal + seasonTotal
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{
			{
				Header: "Draft",
				Bars:   []shared.ProgressBar{{Total: draftTotal}},
			},
			{
				Header: "Season",
				Bars:   []shared.ProgressBar{{Total: seasonTotal}},
			},
		},
	}
}

// registerQueryHandlers wires the workflow's read-side query API.
// The handlers close over &state and &in so they reflect the current
// state without us having to re-register on mutation.
func registerQueryHandlers(ctx workflow.Context, state *simState, in *SimPoolWorkflowInput) error {
	if err := workflow.SetQueryHandler(ctx, QuerySummary, func() (PoolSummary, error) {
		return PoolSummary{
			Status:          string(state.CurrentStatus),
			SimDate:         in.SimDate,
			DayCount:        in.DayCount,
			TotalLLMCostUsd: state.TotalLLMCostUsd,
		}, nil
	}); err != nil {
		return err
	}
	// V1: latest_standings, standings_for_date, rosters, log are
	// implemented as activity-backed queries through GraphQL
	// resolvers reading sim_standings / sim_rosters / sim_transactions
	// directly. Registering empty placeholders here so the query
	// surface is contract-stable; resolvers query Postgres directly.
	if err := workflow.SetQueryHandler(ctx, QueryLatestStandings, func() ([]sqlcdb.SimStanding, error) {
		return nil, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, QueryStandingsForDate, func(date pgtype.Date) ([]sqlcdb.SimStanding, error) {
		return nil, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, QueryRosters, func() ([]sqlcdb.SimRoster, error) {
		return nil, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, QueryLog, func(limit int) ([]sqlcdb.SimTransaction, error) {
		return nil, nil
	}); err != nil {
		return err
	}
	return nil
}

// ============================================================================
// Phase 1 — Draft
// ============================================================================

// runTeamNamePhase fires PickTeamName for every agent in parallel
// and waits for all to complete. Per-agent pauses honor
// PoolConfig.PauseAfterEachTeamName; a post-phase pause honors
// PauseAfterTeamNamePhase. Failures bubble up — without a committed
// team name we don't proceed to the draft.
//
// "After each team name selection" is interpreted as per-agent, but
// since activities fan out in parallel the gate fires as each future
// resolves (in completion order, not dispatch order). Operators get
// one advance per agent; the order is whichever agent's LLM responded
// first, second, etc. Determinism note: replay sees the same Future
// completion order as the original run (Temporal records it).
func runTeamNamePhase(ctx workflow.Context, state *simState) error {
	ctx = workflow.WithActivityOptions(ctx, llmActivityOptions())
	var acts *Activities
	futures := make([]workflow.Future, len(state.AgentIDs))
	for i, agentID := range state.AgentIDs {
		futures[i] = workflow.ExecuteActivity(ctx, acts.PickTeamName, PickTeamNameInput{
			PoolID:      state.PoolID,
			AgentID:     agentID,
			AgentConfig: state.Agents[i],
			NumTeams:    state.PoolConfig.NumTeams,
		})
	}
	for i, f := range futures {
		var res PickTeamNameResult
		if err := f.Get(ctx, &res); err != nil {
			return fmt.Errorf("pick team name for agent %d: %w", state.AgentIDs[i], err)
		}
		state.TotalLLMCostUsd += res.CostUsd
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// runDraftPhase runs the snake-order draft for every roster spot.
// Sequential per-pick (LLM agents take turns, snake order); cancel
// is observed between picks via ctx.Err().
func runDraftPhase(
	ctx workflow.Context,
	state *simState,
	tracker *shared.ReportTracker,
) error {
	logger := workflow.GetLogger(ctx)
	tracker.StartGroup(ctx, 0)

	candidates, err := loadDraftCandidates(ctx, *state)
	if err != nil {
		return err
	}

	// Randomize draft order via SideEffect-captured seed. SideEffect
	// runs fn once and records the result in event history; replay
	// reads it back instead of re-running fn, so the shuffle is
	// deterministic.
	round1 := append([]int32(nil), state.AgentIDs...)
	if err := shuffleAgents(ctx, round1, "draft-order"); err != nil {
		return err
	}
	// Persist the shuffled order to sim_agents.draft_position so the
	// API/CLI can show "currently picking" without querying the workflow.
	var actsForOrder *Activities
	recordCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())
	if err := workflow.ExecuteActivity(recordCtx, actsForOrder.RecordDraftOrder, RecordDraftOrderInput{AgentIDs: round1}).Get(ctx, nil); err != nil {
		return fmt.Errorf("record draft order: %w", err)
	}

	rounds := state.PoolConfig.DraftRounds
	if rounds <= 0 {
		// Defensive: a zero-round draft is a configuration error,
		// but we don't want to deadlock — log + skip.
		logger.Warn("DraftRounds is zero — skipping Phase 1")
		tracker.CompleteGroup(ctx, 0, "Draft skipped (no rounds configured)")
		return nil
	}

	taken := map[int64]struct{}{}
	picksByAgent := map[int32][]int64{}
	notesByAgent := map[int32]string{}
	for i, id := range state.AgentIDs {
		if i < len(state.AgentNotes) {
			notesByAgent[id] = state.AgentNotes[i]
		}
	}
	candidateByID := buildCandidateLookup(candidates)
	pickNumber := 0

	// Snake order built from round1 ordering.
	round1Int64 := make([]int64, len(round1))
	for i, id := range round1 {
		round1Int64[i] = int64(id)
	}
	pickOrder := SnakeDraftOrder(round1Int64, rounds)

	var acts *Activities
	for _, agentID64 := range pickOrder {
		if err := ctx.Err(); err != nil {
			return err
		}

		pickNumber++
		round := ((pickNumber - 1) / state.PoolConfig.NumTeams) + 1
		pick := ((pickNumber - 1) % state.PoolConfig.NumTeams) + 1
		agentID := int32(agentID64)

		input := assembleDraftPickInput(*state, agentID, int32(round), int32(pick), candidates, taken, picksByAgent[agentID], notesByAgent[agentID], candidateByID)
		ctx2 := workflow.WithActivityOptions(ctx, llmActivityOptions())
		var res DraftPickResult
		if err := workflow.ExecuteActivity(ctx2, acts.DraftPick, input).Get(ctx, &res); err != nil {
			return fmt.Errorf("draft pick %d (agent %d): %w", pickNumber, agentID64, err)
		}
		if res.PlayerID > 0 {
			taken[res.PlayerID] = struct{}{}
			picksByAgent[agentID] = append(picksByAgent[agentID], res.PlayerID)
		}
		// The activity may have updated notes via the agent's
		// `update_notes` tool calls. Capture the latest so the next
		// pick's prompt reflects what the agent wrote.
		if res.NotesUpdated {
			notesByAgent[agentID] = res.Notes
		}
		state.TotalLLMCostUsd += res.CostUsd
		tracker.IncrementBar(ctx, 0, 0)

		// Cost-cap exit: the first tripped pick already wrote the
		// cost_cap_reached row and sent the pause signal. Mirror the
		// season loop's pattern — stop executing picks so subsequent
		// picks don't each independently re-run runCostCapBranch and
		// flood the audit table with duplicate rows.
		if res.SkipReason == SkipReasonCostCapReached {
			logger.Info("Draft phase exit (cost cap reached)", "pick_number", pickNumber)
			return nil
		}
	}

	tracker.CompleteGroup(ctx, 0, fmt.Sprintf("Draft complete in %s", tracker.GetElapsed(ctx, 0)))
	return nil
}

// loadDraftCandidates fetches the V1-stub candidate lists. See
// state_activity.go's LoadDraftCandidates note for the production
// gap.
func loadDraftCandidates(ctx workflow.Context, state simState) (LoadDraftCandidatesResult, error) {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	var acts *Activities
	var res LoadDraftCandidatesResult
	if err := workflow.ExecuteActivity(ctx, acts.LoadDraftCandidates, LoadDraftCandidatesInput{
		Season: int32(state.PoolConfig.Season),
	}).Get(ctx, &res); err != nil {
		return LoadDraftCandidatesResult{}, err
	}
	return res, nil
}

// buildCandidateLookup indexes candidates by player ID so the workflow
// can resolve a freshly-drafted PlayerID back to (Name, Position) for
// the per-agent DraftRosterRow without an extra DB roundtrip.
func buildCandidateLookup(candidates LoadDraftCandidatesResult) map[int64]DraftablePlayer {
	out := make(map[int64]DraftablePlayer, len(candidates.Skaters)+len(candidates.Goalies))
	for _, s := range candidates.Skaters {
		out[s.PlayerID] = DraftablePlayer{
			Player:     s.Name,
			ID:         s.PlayerID,
			Position:   s.Position,
			LastSeason: SkaterStats{G: s.PriorG, A: s.PriorA},
		}
	}
	for _, g := range candidates.Goalies {
		out[g.PlayerID] = DraftablePlayer{
			Player:     g.Name,
			ID:         g.PlayerID,
			Position:   "G",
			LastSeason: GoalieStats{W: g.PriorW, GA: g.PriorGA},
		}
	}
	return out
}

// computeSlotsRemaining returns "starter slots this agent still needs
// to fill" — PoolConfig.RosterPositions positional buckets minus what
// the agent has already drafted at each NHL position. Util/BN/IR are
// excluded since they're position-agnostic and don't represent a
// strategic gap during the draft.
func computeSlotsRemaining(cfg PoolConfig, picks []int64, lookup map[int64]DraftablePlayer) map[string]int {
	out := map[string]int{}
	for slot, n := range cfg.RosterPositions {
		switch slot {
		case SlotC, SlotLW, SlotRW, SlotD, SlotG:
			out[string(slot)] = n
		}
	}
	for _, pid := range picks {
		dp, ok := lookup[pid]
		if !ok {
			continue
		}
		if out[dp.Position] > 0 {
			out[dp.Position]--
		}
	}
	return out
}

// assembleDraftPickInput constructs the input to one DraftPickActivity
// call. The DraftPrompt's candidate lists are computed here via
// SelectAvailableByPosition over the workflow-loaded candidates minus
// the per-pool `taken` set; YourRoster / SlotsRemaining come from
// picksByAgent (agent-local draft state) joined against the candidate
// lookup.
func assembleDraftPickInput(
	state simState,
	agentID, round, pick int32,
	candidates LoadDraftCandidatesResult,
	taken map[int64]struct{},
	agentPicks []int64,
	agentNotes string,
	lookup map[int64]DraftablePlayer,
) DraftPickInput {
	availableIDs := make([]int64, 0, len(candidates.Skaters)+len(candidates.Goalies))
	for _, s := range candidates.Skaters {
		if _, t := taken[s.PlayerID]; !t {
			availableIDs = append(availableIDs, s.PlayerID)
		}
	}
	for _, g := range candidates.Goalies {
		if _, t := taken[g.PlayerID]; !t {
			availableIDs = append(availableIDs, g.PlayerID)
		}
	}
	takenSlice := make([]int64, 0, len(taken))
	for id := range taken {
		takenSlice = append(takenSlice, id)
	}

	// Locate AgentConfig for this agent_id via the precomputed lookup
	// (built once in loadSimState) rather than scanning AgentIDs.
	agentCfg := state.AgentConfigByID[agentID]

	byPosition, bestAvailable := SelectAvailableByPosition(candidates.Skaters, candidates.Goalies, taken)
	yourRoster := make([]DraftRosterRow, 0, len(agentPicks))
	for i, pid := range agentPicks {
		dp, ok := lookup[pid]
		if !ok {
			continue
		}
		yourRoster = append(yourRoster, DraftRosterRow{
			Player:   dp.Player,
			ID:       dp.ID,
			Position: dp.Position,
			// Drafted players land on BN per "Slot rules"; the agent
			// sets lineup separately on day 1.
			Slot: string(SlotBN),
			Pick: i + 1,
		})
	}

	return DraftPickInput{
		PoolID:      state.PoolID,
		AgentID:     agentID,
		Round:       round,
		Pick:        pick,
		SimDate:     state.SeasonStartDate,
		WorkflowID:  state.WorkflowID,
		PoolConfig:  state.PoolConfig,
		AgentConfig: agentCfg,
		DraftPrompt: DraftPromptInput{
			DraftInfo: DraftInfo{
				Round:          int(round),
				Pick:           int(pick),
				OverallPick:    int((round-1)*int32(state.PoolConfig.NumTeams) + pick),
				TotalPicks:     state.PoolConfig.NumTeams * state.PoolConfig.DraftRounds,
				NextPickIn:     0,
				SnakeDirection: SnakeDirection(int(round)),
			},
			YourRoster:           yourRoster,
			SlotsRemaining:       computeSlotsRemaining(state.PoolConfig, agentPicks, lookup),
			AvailableByPosition:  byPosition,
			BestAvailableOverall: bestAvailable,
			Notes:                agentNotes,
		},
		AvailableIDs:    availableIDs,
		Roster:          RosterState{},
		RosterPositions: map[int64]sqlcdb.PlayerPosition{},
		RankedSkaters:   candidates.Skaters,
		RankedGoalies:   candidates.Goalies,
		Taken:           takenSlice,
	}
}

// ============================================================================
// Phase 2 — Season day loop
// ============================================================================

// continueAsNewError is the sentinel runSeasonPhase returns when it
// decides to roll over the workflow. The caller (SimPoolWorkflow)
// just propagates it; Temporal recognizes the type and starts a
// new execution.
//
// We use workflow.NewContinueAsNewError directly rather than wrapping
// it — wrapping confuses Temporal's identity check.

// runSeasonPhase is the day loop. The bool return is true when the
// loop exited because a cost-cap pause was signaled — the caller must
// NOT complete the pool in that case (the cost-cap activity already
// wrote status='paused'). Returns a ContinueAsNewError when the
// absolute-day count hits the threshold, the cancel error if the
// workflow was cancelled, or (false, nil) when sim_date passes the
// season's end / the MaxSeasonDays cap.
//
// MaxSeasonDays and the ContinueAsNew threshold are both measured
// against days elapsed from SeasonStartDate, NOT in.DayCount —
// in.DayCount resets to 0 on every ContinueAsNew, so a cap >= the
// threshold would otherwise never trip.
func runSeasonPhase(
	ctx workflow.Context,
	in *SimPoolWorkflowInput,
	state *simState,
	tracker *shared.ReportTracker,
	checkPause func() bool,
) (bool, error) {
	logger := workflow.GetLogger(ctx)
	tracker.StartGroup(ctx, 1)

	for {
		if err := ctx.Err(); err != nil {
			logger.Info("Season phase cancelled", "sim_date", in.SimDate.Time)
			// Status flip happens in SimPoolWorkflow's deferred
			// cancel-cleanup hook.
			return false, err
		}

		// Absolute days elapsed since the season start. ContinueAsNew
		// resets in.DayCount to 0 every ContinueAsNewDayThreshold days,
		// so day-based gates measure against this value instead.
		elapsedDays := daysElapsed(state.SeasonStartDate, in.SimDate)

		// End-of-season exit. EITHER the configured season end date
		// has passed, OR the MaxSeasonDays cap has been reached.
		if state.SeasonEndDate.Valid && in.SimDate.Time.After(state.SeasonEndDate.Time) {
			tracker.CompleteGroup(ctx, 1, fmt.Sprintf("Season complete in %s", tracker.GetElapsed(ctx, 1)))
			return false, nil
		}
		if state.PoolConfig.MaxSeasonDays > 0 && elapsedDays >= state.PoolConfig.MaxSeasonDays {
			tracker.CompleteGroup(ctx, 1, fmt.Sprintf("Season capped at %d days", state.PoolConfig.MaxSeasonDays))
			return false, nil
		}

		// Process one day. Bracket with workflow.Now reads to feed
		// the day-duration metric — workflow.Now is deterministic
		// across replay (recorded in event history), so capturing
		// start/end here doesn't break determinism.
		dayStart := workflow.Now(ctx)
		if err := processOneDay(ctx, state, in.SimDate); err != nil {
			return false, fmt.Errorf("process day %s: %w", in.SimDate.Time.Format("2006-01-02"), err)
		}
		dayDuration := workflow.Now(ctx).Sub(dayStart)

		// Telemetry activity — observes the day-duration histogram.
		// A metric-write failure must NOT abort the season, so the
		// activity's own error is intentionally discarded. But a cancel
		// that lands while it runs surfaces via ctx, so we re-check
		// ctx.Err() immediately after and stop before mutating one more
		// day of state (simDate/DayCount/progress bar).
		recordCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())
		var telemActs *Activities
		_ = workflow.ExecuteActivity(recordCtx, telemActs.RecordDayDuration, RecordDayDurationInput{
			PoolID:          in.PoolID,
			DurationSeconds: dayDuration.Seconds(),
		}).Get(ctx, nil)
		if err := ctx.Err(); err != nil {
			logger.Info("Season phase cancelled (after day-duration)", "sim_date", in.SimDate.Time)
			return false, err
		}

		in.SimDate = addOneDay(in.SimDate)
		in.DayCount++
		tracker.IncrementBar(ctx, 1, 0)

		// Cost-cap exit between days. The cost-cap activity already
		// flipped sim_pools.status to 'paused' in the DB; we just
		// need to stop running — and signal the caller NOT to
		// complete the pool (which would clobber 'paused').
		if checkPause() {
			logger.Info("Season phase exit (cost cap reached)", "sim_date", in.SimDate.Time)
			return true, nil
		}

		if in.DayCount >= ContinueAsNewDayThreshold {
			next := SimPoolWorkflowInput{
				PoolID:   in.PoolID,
				SimDate:  in.SimDate,
				DayCount: 0,
			}
			logger.Info("Phase 2 ContinueAsNew", "sim_date", next.SimDate.Time)
			return false, workflow.NewContinueAsNewError(ctx, SimPoolWorkflow, next)
		}
	}
}

// daysElapsed returns the number of whole calendar days between start
// and current (current − start). Both are sim dates; the result is
// the absolute day index into the season, stable across ContinueAsNew
// boundaries (unlike SimPoolWorkflowInput.DayCount, which resets to 0
// on every rollover). Returns 0 when either date is invalid.
func daysElapsed(start, current pgtype.Date) int {
	if !start.Valid || !current.Valid {
		return 0
	}
	return int(current.Time.Sub(start.Time).Hours() / hoursPerDay)
}

// processOneDay runs the activity sequence for one calendar day.
// Order matches PLAN.md > "Day Loop":
//
//  1. ProcessWaiversActivity (claims due today)
//  2. BuildFreeAgentPoolActivity (one query, shared across agents)
//  3. ManageRosterActivity per agent (in randomized order)
//  4. CollectDayStatsActivity (only if FINAL games on date)
//  5. UpdateStandingsActivity (only if step 4 ran)
//
// V1 simplification: ManageRosterActivity inputs are stubbed
// minimally; the full daily-context builder is a follow-up commit.
func processOneDay(
	ctx workflow.Context,
	state *simState,
	simDate pgtype.Date,
) error {
	var acts *Activities
	dbCtx := workflow.WithActivityOptions(ctx, defaultActivityOptions())
	llmCtx := workflow.WithActivityOptions(ctx, llmActivityOptions())

	if err := workflow.ExecuteActivity(dbCtx, acts.ProcessWaivers, ProcessWaiversInput{
		PoolID:  state.PoolID,
		SimDate: simDate,
	}).Get(ctx, nil); err != nil {
		return fmt.Errorf("process waivers: %w", err)
	}

	var faRes BuildFreeAgentPoolResult
	if err := workflow.ExecuteActivity(dbCtx, acts.BuildFreeAgentPool, BuildFreeAgentPoolInput{
		PoolID:     state.PoolID,
		Season:     int32(state.PoolConfig.Season),
		SimDate:    simDate,
		WaiverDays: int32(state.PoolConfig.WaiverDays),
	}).Get(ctx, &faRes); err != nil {
		return fmt.Errorf("build free agent pool: %w", err)
	}

	// Randomize agent processing order — same SideEffect mechanism
	// as Phase 1. The "kind" string differentiates this seed in the
	// event history from the draft-order one.
	order := append([]int32(nil), state.AgentIDs...)
	if err := shuffleAgents(ctx, order, "daily-agent-order"); err != nil {
		return err
	}

	for _, agentID := range order {
		agentCfg := state.AgentConfigByID[agentID]
		var input ManageRosterInput
		if err := workflow.ExecuteActivity(dbCtx, acts.BuildManageRosterContext, BuildManageRosterContextInput{
			PoolID:      state.PoolID,
			AgentID:     agentID,
			SimDate:     simDate,
			WorkflowID:  state.WorkflowID,
			PoolConfig:  state.PoolConfig,
			AgentConfig: agentCfg,
			FreeAgents:  faRes.PlayerIDs,
		}).Get(ctx, &input); err != nil {
			return fmt.Errorf("build daily context (agent %d): %w", agentID, err)
		}
		var res ManageRosterResult
		if err := workflow.ExecuteActivity(llmCtx, acts.ManageRoster, input).Get(ctx, &res); err != nil {
			return fmt.Errorf("manage roster (agent %d): %w", agentID, err)
		}
		state.TotalLLMCostUsd += res.CostUsd

		// Cost-cap exit: the first agent to trip the cap already wrote
		// the cost_cap_reached row and sent the pause signal
		// (runCostCapBranch). Mirror the draft loop — stop processing
		// the remaining agents so they don't each independently re-run
		// runCostCapBranch and flood the audit table with duplicate rows
		// + redundant pause signals. The pause signal is picked up by
		// checkPause() at the end of the day's iteration.
		if res.SkipReason == SkipReasonCostCapReached {
			workflow.GetLogger(ctx).Info("Daily loop exit (cost cap reached)",
				"sim_date", simDate.Time, "agent_id", agentID)
			return nil
		}
	}

	// CollectDayStats + UpdateStandings only fire if FINAL games
	// landed on this date. CollectDayStats's own no-games branch
	// returns Skipped — but we'd still pay the round-trip and the
	// per-agent loop. Avoid both by leaning on CollectDayStats's
	// SkipReasonNoGames result.
	var collectRes CollectDayStatsResult
	if err := workflow.ExecuteActivity(dbCtx, acts.CollectDayStats, CollectDayStatsInput{
		PoolID:   state.PoolID,
		Season:   int32(state.PoolConfig.Season),
		SimDate:  simDate,
		AgentIDs: state.AgentIDs,
	}).Get(ctx, &collectRes); err != nil {
		return fmt.Errorf("collect day stats: %w", err)
	}
	if !collectRes.Skipped {
		if err := workflow.ExecuteActivity(dbCtx, acts.UpdateStandings, UpdateStandingsInput{
			PoolID:   state.PoolID,
			SimDate:  simDate,
			AgentIDs: state.AgentIDs,
		}).Get(ctx, nil); err != nil {
			return fmt.Errorf("update standings: %w", err)
		}
	}
	return nil
}

// assembleManageRosterInput was the V1 in-memory stub; replaced by
// BuildManageRosterContextActivity, which assembles the same input
// from DB state (standings, roster, agent notes). Removed per the
// production-gating follow-up that landed alongside the activity.

// shuffleAgents Fisher-Yates-shuffles ids in place using a seed
// captured via workflow.SideEffect. Each call to SideEffect records
// the seed in event history, so a replay reads back the same seed
// and produces the same shuffle.
//
// kind is a free-form label that disambiguates SideEffects in the
// same workflow execution (Temporal records SideEffects positionally,
// but human-readable telemetry helps when tracing replay-failure
// stack traces).
func shuffleAgents(ctx workflow.Context, ids []int32, kind string) error {
	enc := workflow.SideEffect(ctx, func(ctx workflow.Context) any {
		return workflow.Now(ctx).UnixNano()
	})
	var seed int64
	if err := enc.Get(&seed); err != nil {
		return fmt.Errorf("capture %s seed: %w", kind, err)
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	return nil
}

// addOneDay returns a pgtype.Date one calendar day after base. Used
// by the day loop's simDate progression — a thin wrapper so the day
// arithmetic is one obvious place to look for off-by-one bugs.
func addOneDay(base pgtype.Date) pgtype.Date {
	if !base.Valid {
		return base
	}
	return pgtype.Date{Time: base.Time.AddDate(0, 0, 1), Valid: true}
}

// ============================================================================
// Phase 3 — Complete
// ============================================================================

// completePool flips status to 'complete' and exits cleanly. Used by
// the normal end-of-season path, which has StartGroup-ed the Season
// group (group 1) so CompleteGroup on it is well-formed.
func completePool(ctx workflow.Context, poolID int32, tracker *shared.ReportTracker) error {
	tracker.CompleteGroup(ctx, 1, fmt.Sprintf("Season complete in %s", tracker.GetElapsed(ctx, 1)))
	return writePoolStatus(ctx, poolID, PoolStatusComplete)
}

// completePoolStatusOnly flips the pool to 'complete' WITHOUT touching
// the Season progress group. Used by the pre-season StopAfter exits
// (StopAfterTeamName, StopAfterDraft), which never StartGroup-ed the
// Season group: CompleteGroup(ctx, 1) there would emit a bogus "Season
// complete" event and read an unstarted (zero) group timer.
func completePoolStatusOnly(ctx workflow.Context, poolID int32) error {
	return writePoolStatus(ctx, poolID, PoolStatusComplete)
}

// writePoolStatus dispatches to SetPoolStatusActivity, which wraps
// UpdateSimPoolStatus. The workflow can't write to Postgres directly
// (deterministic-replay constraint), so status flips after draft,
// on cancel, and on completion all hop through the activity layer.
func writePoolStatus(ctx workflow.Context, poolID int32, status PoolStatus) error {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	var acts *Activities
	return workflow.ExecuteActivity(ctx, acts.SetPoolStatus, SetPoolStatusInput{
		PoolID: poolID,
		Status: status,
	}).Get(ctx, nil)
}

// ============================================================================
// Activity options
// ============================================================================

// defaultActivityOptions cover read-only / fast DB activities.
// Generous timeout because Postgres can occasionally be slow.
func defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	}
}

// llmActivityOptions cover LLM-driven activities (DraftPick, ManageRoster).
// LLM calls are slow (multi-second) and must respect the per-agent
// timeout configured in AgentConfig.TimeoutSeconds. We allow ample
// StartToClose so the activity has room to retry internally before
// Temporal gives up. Retries here are reduced to 2 because LLM
// failures are usually persistent (rate limits, model outage); the
// activity already has its own 1x retry + fallback.
//
// WaitForCancellation=true makes a workflow-level cancel propagate
// into the activity's context.Done(), so an in-flight LLM call can
// abort early instead of running to its StartToCloseTimeout.
func llmActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		WaitForCancellation: true,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    1 * time.Minute,
			MaximumAttempts:    2,
		},
	}
}
