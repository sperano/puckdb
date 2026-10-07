package simulation

// ============================================================================
// PickTeamNameActivity — Phase 0's single paid LLM call.
//
// Asks each agent to commit a team name + strategy summary via the
// set_team_name tool, persists the result, and writes the full
// telemetry trace (sim_agent_turns + children) atomically with the
// durable pool-cost increment. Split out of state_activity.go to match
// the package's one-activity-per-file convention (draft_activity.go,
// manage_activity.go, collect_activity.go, ...).
// ============================================================================

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// PickTeamNameInput carries everything PickTeamName needs to invoke
// an LLM once and persist the chosen name.
type PickTeamNameInput struct {
	PoolID      int32       `json:"pool_id"`
	AgentID     int32       `json:"agent_id"`
	AgentConfig AgentConfig `json:"agent_config"`
	// NumTeams is PoolConfig.NumTeams. The agent built here is cached
	// and reused by the draft and daily turns, so it must carry the
	// real pool size in its system prompt. Zero in activities scheduled
	// before the field existed; those get their own cache entry.
	NumTeams int `json:"num_teams"`
}

// PickTeamNameResult carries the chosen name + cost so the workflow
// can aggregate spend and surface it in metrics.
//
// CostUsd is the spend of THIS invocation only. It is 0 on the
// committed-result path (a previous attempt committed the name and its
// activity response was lost): that attempt already added its cost to
// sim_pools.total_llm_cost_usd inside its own commit, so reporting it
// again would double-count the turn against the pool's cost cap.
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
// Retry safety. The LLM call is the expensive, non-refundable step, so
// everything that could fail around it is arranged to either run
// BEFORE it or ride inside the single commit after it:
//
//  1. Committed-result probe (committedTeamName). sim_agents.team_name
//     is written only on the success path, so a non-empty value means a
//     previous attempt already completed and its response was lost on
//     the way back to Temporal (worker crash between commit and
//     completion report). Return the saved name, skip the model.
//  2. Pre-flight read of record_full_messages. Fetched here rather than
//     in the persist step: a transient DB error on that read used to
//     fail the activity AFTER the tokens were paid for, and the ensuing
//     Temporal retry paid a second time for the same name.
//  3. Atomic commit (persistTeamNameTurn). The team_name write, the
//     durable pool-cost increment and the telemetry trace land in one
//     transaction — all three or none.
//
// Idempotent: RecordTurnTelemetry deletes any prior sim_agent_turns row
// at the same (pool, agent, phase=team_name) coordinate inside the
// same tx before inserting the new one, so a Temporal retry (or a
// manual re-invocation) replaces the previous attempt's row instead of
// colliding with the partial unique index ux_sim_agent_turns_no_date.
// ON DELETE CASCADE removes the prior turn's rounds / tool_calls /
// messages so child PKs don't collide either. Note this telemetry
// replacement makes a retry LOOK clean but does nothing to prevent a
// second paid call — that's the probe's job.
func (a *Activities) PickTeamName(ctx context.Context, in PickTeamNameInput) (PickTeamNameResult, error) {
	logger := activity.GetLogger(ctx)

	committed, err := a.committedTeamName(ctx, in.AgentID)
	if err != nil {
		return PickTeamNameResult{}, err
	}
	if committed != "" {
		logger.Debug("PickTeamName committed-result hit — skipping LLM",
			"pool_id", in.PoolID,
			"agent_id", in.AgentID,
			"name", committed,
		)
		// CostUsd stays 0 — see PickTeamNameResult: the committed
		// attempt's spend is already in sim_pools.total_llm_cost_usd.
		return PickTeamNameResult{Name: committed}, nil
	}

	// Pre-flight the only read the persist path needs, so a DB hiccup
	// can't fail this activity after the model has been paid.
	recordMessages, err := a.fetchRecordMessagesFlag(ctx, in.PoolID)
	if err != nil {
		return PickTeamNameResult{}, err
	}

	turn, err := a.runTeamNameTurn(ctx, in)
	if err != nil {
		return PickTeamNameResult{}, err
	}

	// Persist before branching on the outcome: both failure arms
	// consumed tokens, so their cost and rejected tool calls have to
	// reach the DB even though the activity is about to error.
	header := teamNameTurnHeader(in, turn)
	if err := a.persistTeamNameTurn(ctx, in, header, turn, recordMessages); err != nil {
		return PickTeamNameResult{}, err
	}

	switch {
	case turn.runErr != nil:
		return PickTeamNameResult{CostUsd: turn.costUsd},
			fmt.Errorf("simulation: team-name agent loop: %w", turn.runErr)
	case turn.name == "":
		return PickTeamNameResult{CostUsd: turn.costUsd},
			fmt.Errorf("simulation: team-name response produced no valid name")
	}

	logger.Debug("PickTeamName complete",
		"pool_id", in.PoolID,
		"agent_id", in.AgentID,
		"name", turn.name,
		"cost_usd", turn.costUsd,
		"rounds", len(turn.captures),
	)
	return PickTeamNameResult{Name: turn.name, CostUsd: turn.costUsd}, nil
}

// committedTeamName is PickTeamName's committed-result probe: it reads
// the durable marker the success path writes (sim_agents.team_name) and
// returns "" when no attempt has committed one yet.
//
// One primary-key read, deliberately run before the agent is even
// built, because its entire purpose is to keep a lost activity response
// from re-billing a turn that already succeeded.
//
// A missing agent row is a real error — the workflow only ever asks for
// agent IDs it loaded from sim_agents — so it propagates instead of
// falling through to the LLM.
func (a *Activities) committedTeamName(ctx context.Context, agentID int32) (string, error) {
	agent, err := a.Queries.GetSimAgent(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("simulation: probe committed team name (agent %d): %w", agentID, err)
	}
	return agent.TeamName, nil
}

// teamNameTurn is the in-memory outcome of one team-name agentloop run
// — everything the persist step needs, gathered before any DB write.
// Mirrors draft_activity.go's chooseResult.
type teamNameTurn struct {
	// name / summary are the accepted set_team_name arguments, empty
	// when the agent never made a valid call.
	name    string
	summary string
	// costUsd is the billable spend of this attempt, including the
	// attempts that ended up rejected: the provider charged for them.
	costUsd float64

	res          *agentloop.Result
	captures     []roundCapture
	toolCaptures []ToolCallCapture

	startedAt   time.Time
	completedAt time.Time

	// runErr is agentloop.Run's error. Carried on the struct rather
	// than returned so the caller persists the (already paid for)
	// attempt before surfacing the failure.
	runErr error
}

// runTeamNameTurn builds the agent and runs one team-name agentloop
// turn. The returned error covers ONLY the pre-LLM failure (agent
// construction); anything the LLM call itself produced — its error
// included — rides on the teamNameTurn so the caller can persist the
// attempt first.
func (a *Activities) runTeamNameTurn(ctx context.Context, in PickTeamNameInput) (teamNameTurn, error) {
	agent, err := a.getOrCreateAgent(in.PoolID, in.AgentID, in.AgentConfig, in.NumTeams)
	if err != nil {
		return teamNameTurn{}, fmt.Errorf("simulation: build agent for team-name pick: %w", err)
	}

	messages := []llm.Message{{Role: "user", Content: teamNamePrompt(in.AgentConfig.Strategy)}}

	// Per-turn latency recorder + tool-call accumulator. agentloop.Run
	// calls back into the collector for each tool the LLM emits.
	recorder := newLatencyRecordingClient(agent.Client)
	collector := &teamNameCollector{}

	startedAt := time.Now()
	res, runErr := agentloop.Run(ctx, recorder, messages, collector.execute, agentloop.Config{
		Tools:         TeamNameTools(),
		MaxToolRounds: MaxTeamNameToolRounds,
		MaxTokens:     in.AgentConfig.MaxTokens,
		Temperature:   in.AgentConfig.Temperature,
	})
	completedAt := time.Now()

	turn := teamNameTurn{
		name:         collector.name,
		summary:      collector.summary,
		costUsd:      costFromAggregate(in.AgentConfig, res),
		res:          res,
		captures:     recorder.Captures(),
		toolCaptures: collector.captures,
		startedAt:    startedAt,
		completedAt:  completedAt,
		runErr:       runErr,
	}
	if res != nil {
		AssignRoundsFromAudit(turn.toolCaptures, res.Audit)
	}
	return turn, nil
}

// teamNamePrompt is the single user message for the team-name turn.
// The system prompt comes from the Agent; this only states the one
// action available and the shape of its two arguments.
func teamNamePrompt(strategy string) string {
	return fmt.Sprintf(
		"You are about to start a fantasy hockey simulation. "+
			"Your strategy: %s\n\n"+
			"Call the set_team_name tool exactly once with TWO arguments:\n"+
			"  - name: a memorable team name (1-50 chars) reflecting your strategy or persona\n"+
			"  - summary: a very terse strategy label (<= %d chars) for display in dashboards",
		strategy, MaxStrategySummaryChars,
	)
}

// teamNameCollector accumulates the per-tool-call telemetry for one
// team-name turn plus the (name, summary) pair the agent committed.
//
// Not safe for concurrent use — agentloop.Run drives a turn's tool
// calls serially, so execute never runs twice at once.
type teamNameCollector struct {
	captures []ToolCallCapture
	name     string
	summary  string
}

// execute is the agentloop tool executor. Every arm returns a nil
// error: a rejected call is FEEDBACK to the model, which gets one more
// round to correct itself (MaxTeamNameToolRounds), not an activity
// failure. Returning an error here would abort the loop and throw away
// the tokens already paid for.
func (c *teamNameCollector) execute(_ context.Context, call llm.ToolCall) (string, error) {
	start := time.Now()
	capture := ToolCallCapture{
		ToolName:     call.Function.Name,
		ArgumentsRaw: call.Function.Arguments,
	}

	action, dist, recErr := RecoverAction(call, TeamNameTools())
	if recErr != nil {
		return c.reject(capture, start, ToolCallOutcomeParseError,
			recErr.Error(), fmt.Sprintf("error: %v", recErr))
	}
	capture.RecoveredName = RecoveredName(call.Function.Name, action, dist)

	args, ok := action.(SetTeamNameArgs)
	if !ok {
		reason := fmt.Sprintf("expected %s, got %s", ToolSetTeamName, action.ToolName())
		return c.reject(capture, start, ToolCallOutcomeUnhandled,
			reason, fmt.Sprintf("error: %s", reason))
	}

	name := strings.TrimSpace(args.Name)
	summary := strings.TrimSpace(args.Summary)
	switch {
	case name == "":
		return c.reject(capture, start, ToolCallOutcomeValidationRejected,
			"team name is empty after trim", "error: team name is empty after trim")
	case summary == "":
		return c.reject(capture, start, ToolCallOutcomeValidationRejected,
			"summary is empty after trim",
			fmt.Sprintf("error: summary is required (very terse strategy label, <= %d chars)", MaxStrategySummaryChars))
	case len(summary) > MaxStrategySummaryChars:
		return c.reject(capture, start, ToolCallOutcomeValidationRejected,
			fmt.Sprintf("summary too long (%d chars, max %d)", len(summary), MaxStrategySummaryChars),
			fmt.Sprintf("error: summary must be <= %d chars", MaxStrategySummaryChars))
	}

	c.name = name
	c.summary = summary
	capture.Outcome = ToolCallOutcomeAccepted
	capture.Result = "ok: team name set"
	capture.Latency = time.Since(start)
	c.captures = append(c.captures, capture)
	return capture.Result, nil
}

// reject records a rejected tool call and hands its error string back
// to the model as the tool result, so the next round can see why the
// call didn't stick.
func (c *teamNameCollector) reject(
	capture ToolCallCapture,
	start time.Time,
	outcome ToolCallOutcome,
	reason, result string,
) (string, error) {
	capture.Outcome = outcome
	capture.FailureReason = reason
	capture.Result = result
	capture.Latency = time.Since(start)
	c.captures = append(c.captures, capture)
	return result, nil
}

// teamNameTurnHeader builds the TurnHeader for a team-name turn.
// Status is ok only when the loop finished AND the agent committed a
// name; both error arms still persist the attempt (with its cost and
// its rejected tool calls) so an operator can see what the agent did
// instead of choosing a name.
//
// A turn whose tool call landed but whose loop then errored is treated
// as errored, i.e. the name is NOT committed — the activity failed, so
// the retry should genuinely re-run rather than adopt a half-finished
// turn's name.
func teamNameTurnHeader(in PickTeamNameInput, turn teamNameTurn) TurnHeader {
	header := TurnHeader{
		PoolID:      in.PoolID,
		AgentID:     in.AgentID,
		Phase:       TurnPhaseTeamName,
		Status:      TurnStatusOK,
		Provider:    in.AgentConfig.Provider,
		Model:       in.AgentConfig.Model,
		Temperature: in.AgentConfig.Temperature,
		MaxTokens:   in.AgentConfig.MaxTokens,
		CostUsd:     turn.costUsd,
		StartedAt:   turn.startedAt,
		CompletedAt: turn.completedAt,
	}
	if turn.res != nil && turn.res.Final != nil {
		header.FinalText = turn.res.Final.Content
	}
	switch {
	case turn.runErr != nil:
		header.Status = TurnStatusErrored
		header.ErrorKind = ErrorKindLLMError
		header.ErrorDetail = turn.runErr.Error()
	case turn.name == "":
		// The loop terminated without a successful set_team_name call.
		header.Status = TurnStatusErrored
		header.ErrorKind = ErrorKindToolUseFailure
		header.ErrorDetail = "no successful set_team_name call"
	}
	return header
}

// persistTeamNameTurn writes the team-name UPDATE (success path only),
// the durable pool-cost increment AND the full telemetry trace in one
// atomic-commit block.
//
// All three belong in the same tx because they're one fact: "this agent
// spent this much and got this name". A crash between them would leave
// sim_agents.team_name set without its telemetry row, or — the reason
// the increment moved in here — a name committed whose LLM spend never
// reached sim_pools.total_llm_cost_usd. That total feeds the cost cap
// AND is what the workflow reloads into TotalLLMCostUsd after a
// ContinueAsNew, so a missing increment silently drops the spend for
// the remainder of the run. Same structure as commitDraftPick and the
// daily-turn commit.
//
// Accounting policy for errored turns: an attempt that consumed tokens
// is real spend, so it increments the pool total too. The telemetry row
// is delete-and-replace per (pool, agent, phase), so
// sim_agent_turns.cost_usd reflects only the LATEST attempt while
// sim_pools.total_llm_cost_usd accumulates every paid one — after a
// retry the two intentionally disagree, and the pool total is the one
// that must not undercount.
//
// The increment is unconditional (no `cost > 0` guard), matching
// commitDraftPick and commitDailyTurn: numericFromFloat encodes 0
// cleanly and IncrementSimPoolLLMCost adds it as a no-op, so branching
// on the amount would only give free/unpriced models a second write
// path to get wrong.
func (a *Activities) persistTeamNameTurn(
	ctx context.Context,
	in PickTeamNameInput,
	header TurnHeader,
	turn teamNameTurn,
	recordMessages bool,
) error {
	costNum, err := numericFromFloat(turn.costUsd)
	if err != nil {
		return fmt.Errorf("simulation: encode team-name cost: %w", err)
	}

	messages := transcriptForRecording(recordMessages, turn.res)

	return a.Tx.InTx(ctx, func(q SimQueries) error {
		// team_name is written ONLY on the ok path: it doubles as this
		// activity's committed-result marker, so setting it for an
		// errored turn would make the next retry adopt a name that was
		// never really chosen.
		if header.Status == TurnStatusOK {
			if err := q.SetSimAgentTeamNameAndSummary(ctx, sqlcdb.SetSimAgentTeamNameAndSummaryParams{
				ID:              in.AgentID,
				TeamName:        turn.name,
				StrategySummary: turn.summary,
			}); err != nil {
				return fmt.Errorf("persist team_name+summary for agent %d: %w", in.AgentID, err)
			}
		}
		if _, err := q.IncrementSimPoolLLMCost(ctx, sqlcdb.IncrementSimPoolLLMCostParams{
			ID:              in.PoolID,
			TotalLLMCostUSD: costNum,
		}); err != nil {
			return fmt.Errorf("increment llm cost: %w", err)
		}
		if _, err := RecordTurnTelemetry(ctx, q, TurnTelemetry{
			Header:         header,
			Captures:       turn.captures,
			ToolCalls:      turn.toolCaptures,
			Messages:       messages,
			RecordMessages: recordMessages,
		}); err != nil {
			return err
		}
		return nil
	})
}
