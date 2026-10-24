package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draftrank"
)

const (
	draftOverridesToolName = "get_draft_overrides"
	// Arguments of get_draft_overrides besides league, season and limit.
	draftPlayerKeyArg       = "player_key"
	draftIncludeInactiveArg = "include_inactive"
	// defaultDraftOverridesLimit is the number of overrides returned
	// without a limit argument.
	defaultDraftOverridesLimit = 50
	// maxDraftOverridesLimit caps limit to bound the result size: overrides
	// are never deleted, so include_inactive lists a league's whole history.
	maxDraftOverridesLimit = 500
	// maxDraftOverrideTextRunes bounds the manager-written reason and reset
	// reason of each row.
	maxDraftOverrideTextRunes = 240
)

// draftOverridesLimitParam is the limit of get_draft_overrides.
var draftOverridesLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxDraftOverridesLimit}

// draftOverrideSource is the override read path get_draft_overrides uses;
// *draftrank.Service satisfies it.
type draftOverrideSource interface {
	leagueKeyResolver
	Overrides(ctx context.Context, filter draftrank.OverrideFilter) ([]draftrank.OverrideStatus, error)
}

func draftOverridesTool(source draftOverrideSource, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(draftOverridesToolName,
			mcp.WithDescription("List the manager overrides of a Yahoo league's draft ranking, newest first: "+
				"missed-games replacements, projection-input replacements and excluded news events. "+
				"Read only; overrides are created and reset through GraphQL. "+
				"Lists the league's own overrides and those covering every league (empty league_key); "+
				"an empty scenario covers every scenario. "+
				"state is active, expired (reached expires_at) or reset (ended by a manager at reset_at). "+
				"A new or reset override takes effect at the league's next ranking refresh. "+
				fmt.Sprintf("Reasons are cut to %d characters.", maxDraftOverrideTextRunes)),
			mcp.WithString(leagueArg, mcp.Required(), mcp.Description("Yahoo league key (465.l.1001) or numeric ID (1001); an ID is resolved within season")),
			mcp.WithNumber(seasonYearParam.name, mcp.Required(), mcp.Description("Season as a start year (2025) or a season ID (20252026)")),
			mcp.WithString(draftPlayerKeyArg, mcp.Description("Only this player's overrides (Yahoo player key, e.g. 465.p.1234)")),
			mcp.WithBoolean(draftIncludeInactiveArg, mcp.Description("Also list expired and reset overrides (default false: active only)")),
			mcp.WithNumber(draftOverridesLimitParam.name, mcp.Description(fmt.Sprintf(
				"Maximum overrides returned, newest first (default %d, max %d)", defaultDraftOverridesLimit, maxDraftOverridesLimit))),
		),
		Handler: guard.scopedKey(source, func(ctx context.Context, req mcp.CallToolRequest, ref draftrank.LeagueRef) (*mcp.CallToolResult, error) {
			filter, errResult := draftOverrideFilter(req, ref.LeagueKey)
			if errResult != nil {
				return errResult, nil
			}
			limit, errResult := boundedLimitOrDefault(req, draftOverridesLimitParam, defaultDraftOverridesLimit)
			if errResult != nil {
				return errResult, nil
			}
			overrides, err := source.Overrides(ctx, filter)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(draftOverrideRows(overrides, int(limit)))
		}),
	}
}

// draftOverrideFilter reads player_key and include_inactive. A player key
// is checked against the league's game key, so a mistyped key is refused
// instead of matching nothing.
func draftOverrideFilter(req mcp.CallToolRequest, leagueKey string) (draftrank.OverrideFilter, *mcp.CallToolResult) {
	filter := draftrank.OverrideFilter{LeagueKey: leagueKey}
	playerKey, errResult := optionalString(req, draftPlayerKeyArg)
	if errResult != nil {
		return filter, errResult
	}
	if playerKey = strings.TrimSpace(playerKey); playerKey != "" {
		if err := validateDraftPlayerKey(playerKey, leagueKey); err != nil {
			return filter, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: %v", draftPlayerKeyArg, formatRaw(playerKey), err))
		}
		filter.PlayerKey = playerKey
	}
	filter.IncludeInactive, errResult = optionalBool(req, draftIncludeInactiveArg)
	return filter, errResult
}

// draftOverrideRow is one manager override and its state now.
type draftOverrideRow struct {
	ID          string    `json:"id"`
	PlayerKey   string    `json:"player_key"`
	LeagueKey   string    `json:"league_key"`
	Kind        string    `json:"kind"`
	EventID     string    `json:"event_id"`
	Scenario    string    `json:"scenario"`
	Input       string    `json:"input"`
	Value       float64   `json:"value"`
	State       string    `json:"state"`
	Reason      string    `json:"reason"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResetAt     time.Time `json:"reset_at"`
	ResetReason string    `json:"reset_reason"`
}

// draftOverrideRows keeps the first limit overrides (the service lists
// them newest first) as rows.
func draftOverrideRows(overrides []draftrank.OverrideStatus, limit int) []draftOverrideRow {
	overrides = overrides[:min(len(overrides), limit)]
	rows := make([]draftOverrideRow, len(overrides))
	for i, o := range overrides {
		rows[i] = draftOverrideRow{
			ID: o.ID, PlayerKey: o.PlayerKey, LeagueKey: o.LeagueKey, Kind: string(o.Kind), EventID: o.EventID,
			Scenario: string(o.Scenario), Input: string(o.Input), Value: o.Value, State: string(o.State),
			Reason: boundedText(o.Reason, maxDraftOverrideTextRunes), CreatedBy: o.CreatedBy, CreatedAt: o.CreatedAt,
			ExpiresAt: o.ExpiresAt, ResetAt: o.ResetAt, ResetReason: boundedText(o.ResetReason, maxDraftOverrideTextRunes),
		}
	}
	return rows
}
