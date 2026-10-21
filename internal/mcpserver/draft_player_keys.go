package mcpserver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftwatch"
)

const (
	standInDraftPlayerKeyPrefix = "nhl.p."
	draftPlayerKeyIDBitSize     = 64
)

func requiredDraftPlayerKeys(req mcp.CallToolRequest, leagueKey string) ([]string, *mcp.CallToolResult) {
	raw, present := argument(req, draftPlayerKeysArg)
	if !present {
		return nil, mcp.NewToolResultError(draftPlayerKeysArg + " is required")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want an array of strings", draftPlayerKeysArg, formatRaw(raw)))
	}
	if len(items) == 0 || len(items) > maxDraftPlayerDetailPlayers {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s length %d: want 1 to %d player keys", draftPlayerKeysArg, len(items), maxDraftPlayerDetailPlayers))
	}
	keys := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		key, isString := item.(string)
		key = strings.TrimSpace(key)
		if !isString || key == "" {
			return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s[%d] %s: want a non-empty string", draftPlayerKeysArg, i, formatRaw(item)))
		}
		if err := validateDraftPlayerKey(key, leagueKey); err != nil {
			return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s[%d] %s: %v", draftPlayerKeysArg, i, formatRaw(item), err))
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func validateDraftPlayerKey(playerKey, leagueKey string) error {
	gameKey := draft.GameKeyOf(leagueKey)
	if gameKey > 0 {
		_, err := draftwatch.ParsePlayerKey(playerKey, draftwatch.Identity{LeagueKey: leagueKey, GameKey: gameKey})
		return err
	}
	// Temporary stand-in rankings use stable NHL IDs because their league
	// keys intentionally have no Yahoo game key.
	digits, hasPrefix := strings.CutPrefix(playerKey, standInDraftPlayerKeyPrefix)
	playerID, err := strconv.ParseInt(digits, 10, draftPlayerKeyIDBitSize)
	if !hasPrefix || err != nil || playerID <= 0 {
		return fmt.Errorf("want a stand-in player key like %s8478402", standInDraftPlayerKeyPrefix)
	}
	return nil
}
