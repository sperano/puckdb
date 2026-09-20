package cmd

import (
	"context"
	"encoding/json"
	"fmt"

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
