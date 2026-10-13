package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/graph/model"
)

// ============================================================================
// GraphQLClient extensions for the simulation surface.
//
// Mirrors the pattern used in graphql_workflows.go: each mutation/query
// has a typed method that wraps the GraphQL string with explicit
// argument bindings. The CLI layer in sim.go calls these.
//
// Sim mutations all return a SimPool — much richer than the bool the
// other workflow mutations return — so we can't reuse executeBoolMutation
// directly. The local executeSimPoolMutation helper below handles the
// SimPool unmarshal in one place.
// ============================================================================

// CreateSimPool calls the createSimPool mutation. The input is the
// JSON config file's contents already parsed into the typed input
// struct; this method serializes it back to GraphQL variables.
func (c *GraphQLClient) CreateSimPool(ctx context.Context, input *model.CreateSimPoolInput) (*model.SimPool, error) {
	return c.executeSimPoolMutation(ctx, `mutation($input: CreateSimPoolInput!) {
		createSimPool(input: $input) {
			id
			name
			season
			status
			simDate
			totalLlmCostUsd
			agents { id teamName provider model draftPosition }
		}
	}`, "createSimPool", map[string]any{"input": input})
}

// CancelSimPool cancels the workflow via Temporal RequestCancelWorkflow.
func (c *GraphQLClient) CancelSimPool(ctx context.Context, poolID int) (*model.SimPool, error) {
	return c.executeSimPoolMutation(ctx,
		`mutation($id: Int!) { cancelSimPool(poolId: $id) { id name status simDate totalLlmCostUsd } }`,
		"cancelSimPool",
		map[string]any{"id": poolID})
}

// SimPoolStatusResult bundles the three queries the `puckdb sim
// status` command needs in one round-trip. GraphQL aliases let us
// pull simPool + simPoolProgress + the latest standings inline.
type SimPoolStatusResult struct {
	Pool     *model.SimPool        `json:"pool"`
	Progress *model.ProgressReport `json:"progress"`
}

// SimPoolStatus fetches everything the status command renders. One
// round-trip via aliased fields — server-side joins inside the
// resolver still happen, but we avoid 2-3 separate POSTs.
func (c *GraphQLClient) SimPoolStatus(ctx context.Context, poolID int) (*SimPoolStatusResult, error) {
	resp, err := c.execute(ctx, `query($id: Int!) {
		pool: simPool(id: $id) {
			id
			name
			season
			status
			simDate
			totalLlmCostUsd
			stopAfter
			maxSeasonDays
			startDate
			endDate
			currentDraftAction {
				round
				pick
				totalPicks
				agentId
			}
			agents {
				id
				teamName
				totalRotoPoints
			}
			standings {
				agentId
				category
				value
				rotoPoints
			}
		}
		progress: simPoolProgress(poolId: $id) {
			total
			completed
			groups { header completedMsg startedAt completedAt bars { current total } }
		}
	}`, map[string]any{"id": poolID})
	if err != nil {
		return nil, err
	}
	var out SimPoolStatusResult
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("parse sim status response: %w", err)
	}
	return &out, nil
}

// executeSimPoolMutation is the shared SimPool unmarshal path —
// every sim mutation returns a SimPool under a known field name.
// Returns nil + error when the resolver returned null (e.g.,
// cancelSimPool against a workflow that already exited).
func (c *GraphQLClient) executeSimPoolMutation(ctx context.Context, mutation, field string, variables map[string]any) (*model.SimPool, error) {
	resp, err := c.execute(ctx, mutation, variables)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, fmt.Errorf("parse sim response: %w", err)
	}
	payload, ok := raw[field]
	if !ok || string(payload) == "null" {
		return nil, fmt.Errorf("sim mutation %s returned no data", field)
	}
	var pool model.SimPool
	if err := json.Unmarshal(payload, &pool); err != nil {
		return nil, fmt.Errorf("parse %s SimPool: %w", field, err)
	}
	return &pool, nil
}

// simTurnFeedQuery selects every field `sim tail` prints.
const simTurnFeedQuery = `query($poolId: Int, $afterId: Int, $limit: Int) {
	simTurnFeed(poolId: $poolId, afterId: $afterId, limit: $limit) {
		lastId
		hasMore
		turns {
			id poolId agentId teamName strategySummary model
			phase simDate status errorKind rounds costUsd completedAt
			toolCallCount
			toolCalls {
				roundIndex sequence toolName recoveredName arguments
				outcome failureReason appliedTransactionId playerNames
			}
		}
	}
}`

// simDraftPicksQuery selects every field `sim draft` prints.
const simDraftPicksQuery = `query($poolId: Int!) {
	simDraftPicks(poolId: $poolId) {
		agentId teamName strategySummary model draftPosition
		round pick playerId playerName position reasoning
	}
}`

// SimTurnFeed reads one simTurnFeed page for `sim tail`. A nil poolID
// means every pool; a nil afterID the latest limit turns.
func (c *GraphQLClient) SimTurnFeed(ctx context.Context, poolID, afterID *int, limit int) (tailPage, error) {
	resp, err := c.execute(ctx, simTurnFeedQuery, map[string]any{"poolId": poolID, "afterId": afterID, "limit": limit})
	if err != nil {
		return tailPage{}, c.simFieldError(err, "simTurnFeed", "sim tail")
	}
	var out struct {
		SimTurnFeed *model.SimTurnFeedPage `json:"simTurnFeed"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return tailPage{}, fmt.Errorf("parse simTurnFeed response: %w", err)
	}
	if out.SimTurnFeed == nil {
		return tailPage{}, fmt.Errorf("simTurnFeed returned no data")
	}
	return tailPageFromModel(out.SimTurnFeed)
}

// SimDraftPicks reads a pool's draft picks for `sim draft`; a pool that
// does not exist is errSimPoolNotFound.
func (c *GraphQLClient) SimDraftPicks(ctx context.Context, poolID int) ([]draftRow, error) {
	resp, err := c.execute(ctx, simDraftPicksQuery, map[string]any{"poolId": poolID})
	if err != nil {
		return nil, c.simFieldError(err, "simDraftPicks", "sim draft")
	}
	var out struct {
		SimDraftPicks *[]model.SimDraftPick `json:"simDraftPicks"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return nil, fmt.Errorf("parse simDraftPicks response: %w", err)
	}
	if out.SimDraftPicks == nil {
		return nil, errSimPoolNotFound
	}
	rows := make([]draftRow, len(*out.SimDraftPicks))
	for i, p := range *out.SimDraftPicks {
		rows[i] = draftRowFromModel(p)
	}
	return rows, nil
}

// simFieldError explains the error an API server older than this CLI
// gives for a field it does not have. It matches the message text because
// execute surfaces GraphQL errors as messages only, with no code to test.
func (c *GraphQLClient) simFieldError(err error, field, command string) error {
	if strings.Contains(err.Error(), `Cannot query field "`+field+`"`) {
		return fmt.Errorf("API server at %s predates %s over GraphQL; use a matching puckdb binary: %w", c.endpoint, command, err)
	}
	return err
}

func tailPageFromModel(p *model.SimTurnFeedPage) (tailPage, error) {
	page := tailPage{Entries: make([]tailEntry, len(p.Turns)), LastID: p.LastID, HasMore: p.HasMore}
	for i, t := range p.Turns {
		turn, err := tailTurnFromModel(t)
		if err != nil {
			return tailPage{}, err
		}
		calls := make([]tailToolCall, len(t.ToolCalls))
		for j, c := range t.ToolCalls {
			calls[j] = tailToolCallFromModel(c)
		}
		page.Entries[i] = tailEntry{Turn: turn, Calls: calls}
	}
	return page, nil
}

func tailTurnFromModel(t *model.SimTurnFeedEntry) (tailTurn, error) {
	turn := tailTurn{
		ID:            int64(t.ID),
		PoolID:        t.PoolID,
		AgentID:       int32(t.AgentID),
		TeamName:      t.TeamName,
		Summary:       t.StrategySummary,
		Model:         t.Model,
		Phase:         t.Phase,
		Status:        t.Status,
		ErrorKind:     t.ErrorKind,
		Rounds:        t.Rounds,
		CostUSD:       t.CostUsd,
		CompletedAt:   t.CompletedAt,
		ToolCallCount: t.ToolCallCount,
	}
	if t.SimDate != nil {
		d, err := time.Parse(time.DateOnly, *t.SimDate)
		if err != nil {
			return tailTurn{}, fmt.Errorf("turn %d sim date: %w", t.ID, err)
		}
		turn.SimDate = &d
	}
	return turn, nil
}

func tailToolCallFromModel(c *model.SimTurnToolCall) tailToolCall {
	call := tailToolCall{
		RoundIndex:    c.RoundIndex,
		Sequence:      c.Sequence,
		ToolName:      c.ToolName,
		RecoveredName: c.RecoveredName,
		Arguments:     c.Arguments,
		Outcome:       c.Outcome,
		FailureReason: c.FailureReason,
		PlayerNames:   c.PlayerNames,
	}
	if c.AppliedTransactionID != nil {
		tx := int64(*c.AppliedTransactionID)
		call.AppliedTxID = &tx
	}
	return call
}

func draftRowFromModel(p model.SimDraftPick) draftRow {
	row := draftRow{
		AgentID:    int32(p.AgentID),
		TeamName:   p.TeamName,
		Summary:    p.StrategySummary,
		Model:      p.Model,
		Round:      int32(p.Round),
		Pick:       int32(p.Pick),
		PlayerID:   p.PlayerID,
		PlayerName: p.PlayerName,
		Position:   p.Position,
		Reasoning:  p.Reasoning,
	}
	if p.DraftPosition != nil {
		pos := int32(*p.DraftPosition)
		row.DraftPosition = &pos
	}
	return row
}
