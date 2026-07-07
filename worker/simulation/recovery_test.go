package simulation

import (
	"encoding/json"
	"testing"

	"github.com/sperano/puckdb/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// LevenshteinDistance
// ============================================================================

func TestLevenshteinDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abd", 1},  // substitution
		{"abc", "abcd", 1}, // insertion
		{"abcd", "abc", 1}, // deletion
		{"draft_player", "draft_payer", 1},
		{"draft_player", "draftplayer", 1},
		{"draft_player", "draft-player", 1},
		{"set_lineup", "set_linup", 1},
		{"draft_player", "drop_player", 3}, // genuinely different tools — well above MaxToolNameEditDistance
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, LevenshteinDistance(tc.a, tc.b),
			"distance(%q, %q)", tc.a, tc.b)
	}
}

// ============================================================================
// MatchToolName
// ============================================================================

func TestMatchToolName_ExactMatchShortCircuits(t *testing.T) {
	candidates := []string{ToolDraftPlayer, ToolSetLineup, ToolUpdateNotes}
	got, dist, ok := MatchToolName(ToolSetLineup, candidates)
	require.True(t, ok)
	assert.Equal(t, ToolSetLineup, got)
	assert.Equal(t, 0, dist)
}

func TestMatchToolName_FuzzyOneEdit(t *testing.T) {
	candidates := []string{ToolDraftPlayer, ToolSetLineup, ToolUpdateNotes}
	got, dist, ok := MatchToolName("draft_payer", candidates)
	require.True(t, ok)
	assert.Equal(t, ToolDraftPlayer, got)
	assert.Equal(t, 1, dist)
}

func TestMatchToolName_FuzzyTwoEdits(t *testing.T) {
	candidates := []string{ToolDraftPlayer, ToolSetLineup}
	got, dist, ok := MatchToolName("set_lineu", candidates) // 1 deletion
	require.True(t, ok)
	assert.Equal(t, ToolSetLineup, got)
	assert.Equal(t, 1, dist)
}

// Beyond the threshold → no match. "drop_player" vs "draft_player" is
// distance 4 — that's a different tool, not a typo.
func TestMatchToolName_BeyondThreshold_NoMatch(t *testing.T) {
	candidates := []string{ToolDraftPlayer}
	_, _, ok := MatchToolName("drop_player", candidates)
	assert.False(t, ok, "edit distance > MaxToolNameEditDistance must NOT fuzzy-match (else we'd silently dispatch the wrong tool)")
}

// Closest candidate wins among multiple within-threshold candidates.
func TestMatchToolName_ClosestWins(t *testing.T) {
	candidates := []string{
		"abcdef",  // distance 2 from "abcdez"
		"abcdez",  // distance 0
		"abcdezz", // distance 1
	}
	got, dist, ok := MatchToolName("abcdez", candidates)
	require.True(t, ok)
	assert.Equal(t, "abcdez", got)
	assert.Equal(t, 0, dist)
}

func TestMatchToolName_EmptyCandidates(t *testing.T) {
	_, _, ok := MatchToolName(ToolDraftPlayer, nil)
	assert.False(t, ok)
}

// All six real tool names must be at distance ≥ 3 from each other so
// the fuzzy match can't cross-dispatch. Pin this invariant — if a new
// tool is added at distance ≤ 2 from an existing one, this test fires.
func TestToolNames_AreDistinctEnoughForFuzzyMatching(t *testing.T) {
	names := []string{
		ToolDraftPlayer, ToolSetLineup, ToolAddPlayer,
		ToolClaimPlayer, ToolDropPlayer, ToolUpdateNotes,
	}
	for i, a := range names {
		for j, b := range names {
			if i == j {
				continue
			}
			d := LevenshteinDistance(a, b)
			assert.Greater(t, d, MaxToolNameEditDistance,
				"tools %q and %q are within fuzzy threshold (%d) — fuzzy matching could silently cross-dispatch",
				a, b, d)
		}
	}
}

// ============================================================================
// Lenient JSON
// ============================================================================

func TestLenientUnmarshalJSON_ValidJSONPasses(t *testing.T) {
	var args DraftPlayerArgs
	require.NoError(t, LenientUnmarshalJSON([]byte(`{"player_id": 8478402}`), &args))
	assert.Equal(t, int64(8478402), args.PlayerID)
}

func TestLenientUnmarshalJSON_TrailingCommaInObject(t *testing.T) {
	var args DraftPlayerArgs
	require.NoError(t, LenientUnmarshalJSON([]byte(`{"player_id": 8478402,}`), &args))
	assert.Equal(t, int64(8478402), args.PlayerID)
}

func TestLenientUnmarshalJSON_TrailingCommaInArray(t *testing.T) {
	var args SetLineupArgs
	body := `{"moves":[{"player_id":1,"slot":"C"},{"player_id":2,"slot":"BN"},]}`
	require.NoError(t, LenientUnmarshalJSON([]byte(body), &args))
	require.Len(t, args.Moves, 2)
}

func TestLenientUnmarshalJSON_UnquotedKeys(t *testing.T) {
	var args AddPlayerArgs
	require.NoError(t, LenientUnmarshalJSON([]byte(`{player_id: 1, drop_player_id: 2}`), &args))
	assert.Equal(t, int64(1), args.PlayerID)
	require.NotNil(t, args.DropPlayerID)
	assert.Equal(t, int64(2), *args.DropPlayerID)
}

func TestLenientUnmarshalJSON_BothFixesTogether(t *testing.T) {
	var args AddPlayerArgs
	require.NoError(t, LenientUnmarshalJSON([]byte(`{player_id: 1, drop_player_id: 2,}`), &args))
	assert.Equal(t, int64(1), args.PlayerID)
}

// String values containing `}` or `,` must NOT be touched. This is the
// state-machine-vs-regex test: a regex preprocessor would mangle these.
func TestLenientUnmarshalJSON_StringValueWithBraceAndComma(t *testing.T) {
	var args UpdateNotesArgs
	body := `{"notes": "I want {Kucherov, Draisaitl} on my team"}`
	require.NoError(t, LenientUnmarshalJSON([]byte(body), &args))
	assert.Equal(t, "I want {Kucherov, Draisaitl} on my team", args.Notes)
}

// String values that LOOK like an unquoted key must NOT be requoted.
// `"a:b"` inside a string is just a value with a colon.
func TestLenientUnmarshalJSON_ColonInsideStringValue(t *testing.T) {
	var args UpdateNotesArgs
	body := `{"notes": "format = key:value"}`
	require.NoError(t, LenientUnmarshalJSON([]byte(body), &args))
	assert.Equal(t, "format = key:value", args.Notes)
}

// Escaped quotes inside strings must keep the state machine inside the
// string scope. A naive escape-blind walker would close the string at
// `\"` and start mangling JSON syntax.
func TestLenientUnmarshalJSON_EscapedQuotesPreserveStringContext(t *testing.T) {
	var args UpdateNotesArgs
	body := `{"notes": "she said \"hi\" to me, then left"}`
	require.NoError(t, LenientUnmarshalJSON([]byte(body), &args))
	assert.Equal(t, `she said "hi" to me, then left`, args.Notes)
}

// Cleanup must be IDEMPOTENT — running it on already-clean JSON
// yields the same string. Pin so a future "be more aggressive about
// fixing things" change doesn't silently rewrite valid input.
func TestLenientCleanJSON_IdempotentOnValidInput(t *testing.T) {
	cases := []string{
		`{"a":1}`,
		`{"moves":[{"player_id":1,"slot":"C"}]}`,
		`{"notes": "hi"}`,
		`{}`,
		`[]`,
	}
	for _, in := range cases {
		assert.Equal(t, in, lenientCleanJSON(in), "cleanup must be a no-op on already-valid JSON")
	}
}

// Genuinely malformed JSON (not just trailing-comma / unquoted-key)
// must surface a wrapped error from json.Unmarshal — pin that the
// cleaned payload appears in the error so the failing args are
// debuggable in logs.
func TestLenientUnmarshalJSON_HardErrorIncludesCleanedPayload(t *testing.T) {
	var args DraftPlayerArgs
	err := LenientUnmarshalJSON([]byte(`{this is not even close to json}`), &args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lenient unmarshal")
	assert.Contains(t, err.Error(), "cleaned:", "error must include the cleaned payload to aid debugging")
}

// ============================================================================
// ExtractPlayerIDs
// ============================================================================

func TestExtractPlayerIDs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int64
	}{
		{
			"prose with one ID",
			"I want to draft 8478402 next.",
			[]int64{8478402},
		},
		{
			"multiple IDs in order",
			"Drop 8479337, then add 8478402.",
			[]int64{8479337, 8478402},
		},
		{
			"deduplicated",
			"8478402 and 8478402 again.",
			[]int64{8478402},
		},
		{
			"no IDs in plain prose",
			"This is just text without numbers.",
			nil,
		},
		{
			"6-digit numbers must NOT match (player IDs are exactly 7 digits)",
			"the year 2025 and the count 999999",
			nil,
		},
		{
			"longer numbers must not produce a substring match",
			"game id 2024030001 here",
			nil,
		},
		{
			"two IDs adjacent to punctuation",
			"add(8478402); drop(8479337)",
			[]int64{8478402, 8479337},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractPlayerIDs(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ============================================================================
// RecoverAction — top-level wrapper
// ============================================================================

func recoveryToolCall(name, args string) llm.ToolCall {
	return llm.ToolCall{
		ID:       "rec-1",
		Type:     "function",
		Function: llm.ToolCallFunction{Name: name, Arguments: args},
	}
}

// Layer 1 — already valid. Distance 0, exact path.
func TestRecoverAction_ExactPathReturnsDistanceZero(t *testing.T) {
	call := recoveryToolCall(ToolDraftPlayer, `{"player_id": 8478402}`)
	act, dist, err := RecoverAction(call, DraftTools())
	require.NoError(t, err)
	assert.Equal(t, DraftPlayerArgs{PlayerID: 8478402}, act)
	assert.Equal(t, 0, dist)
}

// Layer 2 — typo'd tool name. Recoverable; distance reports the cost.
func TestRecoverAction_FuzzyToolNameRecovers(t *testing.T) {
	call := recoveryToolCall("draft_payer", `{"player_id": 8478402}`)
	act, dist, err := RecoverAction(call, DraftTools())
	require.NoError(t, err)
	assert.Equal(t, DraftPlayerArgs{PlayerID: 8478402}, act)
	assert.Equal(t, 1, dist)
}

// Layer 3 — args malformed but cleanable. Distance is -1 to flag
// "args were repaired but tool name was exact" — the test pins the
// convention so a downstream metric collector knows what to record.
func TestRecoverAction_LenientArgsReturnNegativeDistance(t *testing.T) {
	call := recoveryToolCall(ToolDraftPlayer, `{player_id: 8478402,}`)
	act, dist, err := RecoverAction(call, DraftTools())
	require.NoError(t, err)
	assert.Equal(t, DraftPlayerArgs{PlayerID: 8478402}, act)
	assert.Equal(t, -1, dist)
}

// Both layers needed — fuzzy name AND lenient JSON. Distance reports
// the fuzzy edit count (positive); the lenient layer's contribution
// is implicit.
func TestRecoverAction_FuzzyNameAndLenientArgs(t *testing.T) {
	call := recoveryToolCall("draft_payer", `{player_id: 8478402,}`)
	act, dist, err := RecoverAction(call, DraftTools())
	require.NoError(t, err)
	assert.Equal(t, DraftPlayerArgs{PlayerID: 8478402}, act)
	assert.Equal(t, 1, dist, "fuzzy distance is reported even when lenient layer also fired")
}

// Unrecoverable — name doesn't fuzzy-match anything legal AND args
// are nonsense. Returns a clear error naming the tool.
func TestRecoverAction_UnrecoverableSurfacesClearError(t *testing.T) {
	call := recoveryToolCall("totally_unrelated_name", `nonsense`)
	_, _, err := RecoverAction(call, DraftTools())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "totally_unrelated_name")
	assert.Contains(t, err.Error(), "cannot recover")
}

// Phase scope — RecoverAction must NOT cross-dispatch tools that
// belong to a different phase. The fuzzy match's candidate list is
// the per-phase tool set; if you pass DraftTools() and the LLM
// emits "set_lineup", recovery must fail rather than silently
// reaching into the daily set.
func TestRecoverAction_RespectsPhaseScope(t *testing.T) {
	// Daily tool name during draft phase — must NOT recover.
	call := recoveryToolCall(ToolSetLineup, `{"moves": []}`)
	_, _, err := RecoverAction(call, DraftTools())
	require.Error(t, err, "set_lineup must not be recoverable when only draft tools are legal")
}

// Belt-and-braces — exact match still works through RecoverAction
// (catches a regression where Layer 1 silently fails).
func TestRecoverAction_AllSixToolsRoundTripExact(t *testing.T) {
	cases := []struct {
		call llm.ToolCall
		want Action
	}{
		{recoveryToolCall(ToolDraftPlayer, `{"player_id":1}`), DraftPlayerArgs{PlayerID: 1}},
		{recoveryToolCall(ToolSetLineup, `{"moves":[]}`), SetLineupArgs{Moves: []LineupMoveArg{}}},
		{recoveryToolCall(ToolAddPlayer, `{"player_id":1}`), AddPlayerArgs{PlayerID: 1}},
		{recoveryToolCall(ToolClaimPlayer, `{"player_id":1}`), ClaimPlayerArgs{PlayerID: 1}},
		{recoveryToolCall(ToolDropPlayer, `{"player_id":1}`), DropPlayerArgs{PlayerID: 1}},
		{recoveryToolCall(ToolUpdateNotes, `{"notes":"hi"}`), UpdateNotesArgs{Notes: "hi"}},
	}
	dailyTools := DailyTools()
	allTools := append(dailyTools, DraftTools()...)
	for _, tc := range cases {
		act, dist, err := RecoverAction(tc.call, allTools)
		require.NoError(t, err, "tool %q", tc.call.Function.Name)
		assert.Equal(t, tc.want, act)
		assert.Equal(t, 0, dist)
	}
}

// Layer 3 must reject malformed JSON that even the lenient cleanup
// can't fix — verify the lenient path doesn't accidentally accept
// nonsense by ignoring residual unmarshal errors.
func TestRecoverAction_LenientCannotSaveTotalGarbage(t *testing.T) {
	call := recoveryToolCall(ToolDraftPlayer, `not json at all`)
	_, _, err := RecoverAction(call, DraftTools())
	require.Error(t, err)
}

// Sanity: lenient cleanup still produces parseable JSON when given
// real-world examples seen in LLM outputs.
func TestLenientCleanJSON_SmokeTest_ParsesAfterCleanup(t *testing.T) {
	cases := []string{
		`{a:1,}`,
		`{a: "x", b: 2,}`,
		`{moves: [{player_id: 1, slot: "C",},]}`,
	}
	for _, in := range cases {
		cleaned := lenientCleanJSON(in)
		var v any
		assert.NoError(t, json.Unmarshal([]byte(cleaned), &v),
			"input %q cleaned to %q must parse", in, cleaned)
	}
}

// ============================================================================
// Low-2: parseActionLenient must handle ToolSetTeamName so layer-3
// recovery succeeds during the team-name phase. Before the fix, the
// default case returned "unknown tool", causing RecoverAction to fail
// on every team-name parse error — unrecoverable during Phase 0.
// ============================================================================

// RecoverAction dispatches through all three layers. Layer 3 (lenient)
// must correctly parse a set_team_name call whose JSON is non-standard
// (trailing comma) — the exact failure mode that triggers recovery.
func TestRecoverAction_SetTeamName_LenientParsesNonStandardJSON(t *testing.T) {
	call := recoveryToolCall(ToolSetTeamName, `{"name": "The Mighty Ducks", "summary": "A classic team",}`)
	act, _, err := RecoverAction(call, TeamNameTools())
	require.NoError(t, err)
	got, ok := act.(SetTeamNameArgs)
	require.True(t, ok, "expected SetTeamNameArgs, got %T", act)
	assert.Equal(t, "The Mighty Ducks", got.Name)
	assert.Equal(t, "A classic team", got.Summary)
}

// parseActionLenient alone: exact JSON. Ensures the ToolSetTeamName
// case branch exists and routes to the right args type.
func TestParseActionLenient_SetTeamName_ExactJSON(t *testing.T) {
	call := recoveryToolCall(ToolSetTeamName, `{"name":"Ice Kings","summary":"We skate hard"}`)
	act, err := parseActionLenient(call)
	require.NoError(t, err)
	got, ok := act.(SetTeamNameArgs)
	require.True(t, ok, "expected SetTeamNameArgs, got %T", act)
	assert.Equal(t, "Ice Kings", got.Name)
	assert.Equal(t, "We skate hard", got.Summary)
}
