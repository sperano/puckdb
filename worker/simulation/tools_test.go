package simulation

import (
	"encoding/json"
	"testing"

	"github.com/sperano/puckdb/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each tool definition's name must match its corresponding constant. A
// typo here (or in the constant) would let the agent's dispatch switch
// quietly fail to recognize the tool — pinning this tightens the
// contract between definition and dispatch.
func TestTools_NamesMatchConstants(t *testing.T) {
	want := map[string]bool{
		ToolDraftPlayer: true,
		ToolSetLineup:   true,
		ToolAddPlayer:   true,
		ToolClaimPlayer: true,
		ToolDropPlayer:  true,
		ToolUpdateNotes: true,
	}
	all := append(DraftTools(), DailyTools()...)
	got := make(map[string]bool)
	for _, tool := range all {
		got[tool.Function.Name] = true
	}
	for name := range want {
		assert.Truef(t, got[name], "tool %q missing from DraftTools+DailyTools", name)
	}
}

// Every tool's Parameters must be syntactically valid JSON Schema. We
// parse as a generic map and assert the top-level shape (object with
// type + properties + required). Avoids pulling in a JSON-Schema
// validator dependency for what's essentially a smoke check.
func TestTools_SchemasAreValidObjects(t *testing.T) {
	all := append(DraftTools(), DailyTools()...)
	for _, tool := range all {
		t.Run(tool.Function.Name, func(t *testing.T) {
			var schema map[string]any
			require.NoError(t, json.Unmarshal(tool.Function.Parameters, &schema),
				"tool %q schema must be valid JSON", tool.Function.Name)
			assert.Equal(t, "object", schema["type"], "schema must be a JSON Schema object")
			assert.NotNil(t, schema["properties"], "schema must declare properties")
			assert.NotNil(t, schema["required"], "schema must declare required fields")
		})
	}
}

// Cacheable boundary: exactly ONE tool in each phase's list is
// Cacheable, and it must be the LAST entry. The Anthropic translator
// places the cache_control marker on the final cacheable block in wire
// order — putting the marker mid-list would shrink the cached prefix.
func TestDraftTools_LastToolIsCacheable(t *testing.T) {
	tools := DraftTools()
	require.NotEmpty(t, tools)
	for i, tool := range tools {
		if i == len(tools)-1 {
			assert.True(t, tool.Cacheable, "last DraftTools entry must be Cacheable")
		} else {
			assert.False(t, tool.Cacheable, "tool %q (index %d) must NOT be Cacheable", tool.Function.Name, i)
		}
	}
}

func TestDailyTools_LastToolIsCacheable(t *testing.T) {
	tools := DailyTools()
	require.NotEmpty(t, tools)
	for i, tool := range tools {
		if i == len(tools)-1 {
			assert.True(t, tool.Cacheable, "last DailyTools entry must be Cacheable")
		} else {
			assert.False(t, tool.Cacheable, "tool %q (index %d) must NOT be Cacheable", tool.Function.Name, i)
		}
	}
}

// Phase composition pin: draft phase has only draft_player + update_notes;
// daily phase has the four roster-management tools + update_notes. The
// content matters because the system prompt explicitly references each
// available tool — a drift here would either hallucinate-the-LLM or
// silently disable a feature.
func TestDraftTools_Composition(t *testing.T) {
	tools := DraftTools()
	require.Len(t, tools, 2)
	assert.Equal(t, ToolDraftPlayer, tools[0].Function.Name)
	assert.Equal(t, ToolUpdateNotes, tools[1].Function.Name)
}

func TestDailyTools_Composition(t *testing.T) {
	tools := DailyTools()
	require.Len(t, tools, 5)
	want := []string{
		ToolSetLineup,
		ToolAddPlayer,
		ToolClaimPlayer,
		ToolDropPlayer,
		ToolUpdateNotes,
	}
	for i, name := range want {
		assert.Equal(t, name, tools[i].Function.Name, "DailyTools[%d]", i)
	}
}

// Every Tool struct must be Type=="function" — the canonical OpenAI
// shape. Anthropic's translator uses the .Function.Name and
// .Function.Parameters; if Type ever drifts to "" the OpenAI client
// would still serialize it but a stricter consumer might reject.
func TestTools_TypeIsFunction(t *testing.T) {
	for _, tool := range append(DraftTools(), DailyTools()...) {
		assert.Equal(t, "function", tool.Type, "tool %q", tool.Function.Name)
	}
}

// ============================================================================
// ParseArgs — round-trip and error paths.
// ============================================================================

func TestParseArgs_DraftPlayer(t *testing.T) {
	call := llm.ToolCall{
		Function: llm.ToolCallFunction{
			Name:      ToolDraftPlayer,
			Arguments: `{"player_id": 8478402}`,
		},
	}
	args, err := ParseArgs[DraftPlayerArgs](call)
	require.NoError(t, err)
	assert.Equal(t, int64(8478402), args.PlayerID)
}

func TestParseArgs_SetLineup(t *testing.T) {
	call := llm.ToolCall{
		Function: llm.ToolCallFunction{
			Name: ToolSetLineup,
			Arguments: `{
				"moves": [
					{"player_id": 1, "slot": "C"},
					{"player_id": 2, "slot": "BN"}
				]
			}`,
		},
	}
	args, err := ParseArgs[SetLineupArgs](call)
	require.NoError(t, err)
	require.Len(t, args.Moves, 2)
	assert.Equal(t, int64(1), args.Moves[0].PlayerID)
	assert.Equal(t, SlotC, args.Moves[0].Slot)
	assert.Equal(t, SlotBN, args.Moves[1].Slot)
}

// drop_player_id is OPTIONAL — pointer field so omission yields nil
// rather than 0. Pin both branches because mistreating "no drop" as
// "drop player 0" would corrupt the roster on every full-roster add.
func TestParseArgs_AddPlayer_OptionalDropPlayer(t *testing.T) {
	t.Run("with drop", func(t *testing.T) {
		call := llm.ToolCall{
			Function: llm.ToolCallFunction{
				Name:      ToolAddPlayer,
				Arguments: `{"player_id": 1, "drop_player_id": 2}`,
			},
		}
		args, err := ParseArgs[AddPlayerArgs](call)
		require.NoError(t, err)
		assert.Equal(t, int64(1), args.PlayerID)
		require.NotNil(t, args.DropPlayerID)
		assert.Equal(t, int64(2), *args.DropPlayerID)
	})
	t.Run("without drop", func(t *testing.T) {
		call := llm.ToolCall{
			Function: llm.ToolCallFunction{
				Name:      ToolAddPlayer,
				Arguments: `{"player_id": 1}`,
			},
		}
		args, err := ParseArgs[AddPlayerArgs](call)
		require.NoError(t, err)
		assert.Equal(t, int64(1), args.PlayerID)
		assert.Nil(t, args.DropPlayerID, "missing drop_player_id must unmarshal to nil, not 0")
	})
}

func TestParseArgs_UpdateNotes(t *testing.T) {
	call := llm.ToolCall{
		Function: llm.ToolCallFunction{
			Name:      ToolUpdateNotes,
			Arguments: `{"notes": "draft plan: target Cs, ignore PIM"}`,
		},
	}
	args, err := ParseArgs[UpdateNotesArgs](call)
	require.NoError(t, err)
	assert.Equal(t, "draft plan: target Cs, ignore PIM", args.Notes)
}

func TestParseArgs_MalformedJSON(t *testing.T) {
	call := llm.ToolCall{
		Function: llm.ToolCallFunction{
			Name:      ToolDraftPlayer,
			Arguments: `{not valid json}`,
		},
	}
	_, err := ParseArgs[DraftPlayerArgs](call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ToolDraftPlayer, "error must name the tool to aid debugging")
}

// Empty Arguments is a known SDK quirk on some retry shims — surface a
// dedicated error instead of letting json.Unmarshal's "unexpected end
// of JSON input" leak.
func TestParseArgs_EmptyArgumentsHasOwnError(t *testing.T) {
	call := llm.ToolCall{
		Function: llm.ToolCallFunction{
			Name:      ToolDraftPlayer,
			Arguments: "",
		},
	}
	_, err := ParseArgs[DraftPlayerArgs](call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty arguments")
}

// Models occasionally pattern-match the tool name into the argument
// name (e.g. set_team_name → "team_name" instead of "name"). Strict
// decoding turns this into a parse_error with the offending field
// surfaced in the message, so the round-loop's tool result tells the
// LLM exactly what to fix instead of bouncing it off the validator
// with the confusing "team name is empty after trim".
func TestParseArgs_UnknownFieldRejectedWithFieldName(t *testing.T) {
	call := llm.ToolCall{
		Function: llm.ToolCallFunction{
			Name:      ToolSetTeamName,
			Arguments: `{"team_name":"Breakout Brigade"}`,
		},
	}
	_, err := ParseArgs[SetTeamNameArgs](call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ToolSetTeamName, "error must name the tool")
	assert.Contains(t, err.Error(), "team_name", "error must name the offending field")
}
