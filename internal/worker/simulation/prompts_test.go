package simulation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// System prompt
// ============================================================================

func TestBuildSystemPrompt_SubstitutesPlaceholders(t *testing.T) {
	got := BuildSystemPrompt(AgentConfig{
		Strategy: "punt PIM, all-in on offense",
	}, 5)

	assert.Contains(t, got, "You are an AI fantasy hockey manager")
	assert.Contains(t, got, "in a 5-team rotisserie pool")
	assert.Contains(t, got, "Your strategy: punt PIM, all-in on offense")
	// No raw placeholders may leak through.
	assert.NotContains(t, got, "{strategy}")
	assert.NotContains(t, got, "{N}")
}

// Byte-stability is the WHOLE POINT of caching this block. Different
// inputs produce different outputs (good); identical inputs produce
// identical outputs (essential — that's the cache hit). Run twice and
// assert string equality.
func TestBuildSystemPrompt_IsByteStable(t *testing.T) {
	agent := AgentConfig{Strategy: "balance all categories"}
	a := BuildSystemPrompt(agent, 5)
	b := BuildSystemPrompt(agent, 5)
	assert.Equal(t, a, b, "two calls with identical inputs must produce byte-identical strings")
}

// Nothing in the system prompt should reference the current day, today's
// date, or any time-varying state — that would invalidate the cache on
// every turn. This is a guard against future drift; if a future edit
// adds "today is {date}" to the template, this test catches it.
func TestSystemPromptTemplate_HasNoTimeVaryingMarkers(t *testing.T) {
	forbidden := []string{
		"{date}", "{day}", "{today}", "{day_count}", "{sim_date}",
		"{round}", "{pick}", "{standings}",
	}
	for _, f := range forbidden {
		assert.NotContains(t, systemPromptTemplate, f,
			"system prompt must stay byte-stable; %q is per-turn data and belongs in the daily/draft context", f)
	}
}

// Empty strategy must not fail or include garbage; the line just reads
// "Your strategy: " with nothing after. Pin both behaviors.
func TestBuildSystemPrompt_EmptyStrategy(t *testing.T) {
	got := BuildSystemPrompt(AgentConfig{}, 5)
	assert.Contains(t, got, "Your strategy: \n", "empty strategy must leave the line stub intact")
	assert.NotContains(t, got, "{strategy}")
}

// TestV1FixedRosterMatchesPrompt pins the V1 fixed-roster assumption
// documented on PoolConfig (types.go) and on v1FixedCategories /
// v1FixedRoster (prompts.go, just above systemPromptTemplate): the
// system prompt's "9 roto categories" line and "Roster: ..." line are
// hard-coded text, not derived from PoolConfig at runtime. If either
// constant changes without the corresponding edit to
// systemPromptTemplate (or vice versa), this test fails — that's the
// point, since a silent divergence here would mean the agent is being
// told a roster/category shape the validators don't actually enforce.
func TestV1FixedRosterMatchesPrompt(t *testing.T) {
	require.Len(t, v1FixedCategories, 9, `systemPromptTemplate says "9 roto categories"`)
	for _, cat := range v1FixedCategories {
		assert.Contains(t, systemPromptTemplate, cat,
			"category %q must appear in the system prompt's category line", cat)
	}

	cfg := PoolConfig{RosterPositions: v1FixedRoster}
	wantRosterLine := fmt.Sprintf(
		"Roster: %dC, %dLW, %dRW, %dD, %dUtil, %dG active | %dBN bench | %dIR",
		cfg.RosterPositions[SlotC], cfg.RosterPositions[SlotLW], cfg.RosterPositions[SlotRW],
		cfg.RosterPositions[SlotD], cfg.RosterPositions[SlotUtil], cfg.RosterPositions[SlotG],
		cfg.RosterPositions[SlotBN], cfg.RosterPositions[SlotIR],
	)
	assert.Contains(t, systemPromptTemplate, wantRosterLine,
		"v1FixedRoster must match the prompt's hard-coded Roster: line")
}

// ============================================================================
// Daily prompt
// ============================================================================

func TestBuildDailyPrompt_RoundTripsAndPreservesSpecialKeys(t *testing.T) {
	input := DailyPromptInput{
		Day: "2025-11-15",
		Standings: []StandingRow{{
			Agent: "Claude", IsYou: true,
			G:            CategoryStanding{Value: 45, Rank: 1},
			A:            CategoryStanding{Value: 82, Rank: 3},
			PlusMinus:    CategoryStanding{Value: 12, Rank: 2},
			PIM:          CategoryStanding{Value: 60, Rank: 4},
			PPP:          CategoryStanding{Value: 20, Rank: 3},
			SOG:          CategoryStanding{Value: 190, Rank: 2},
			W:            CategoryStanding{Value: 8, Rank: 4},
			GA:           CategoryStanding{Value: 30, Rank: 5},
			GAA:          CategoryStanding{Value: 2.5, Rank: 3},
			TotalRotoPts: 38.5,
		}},
		YourRoster: []RosterRow{{
			Player: "Nikita Kucherov", ID: 8476453,
			NHLPosition: "RW", Slot: SlotRW, Team: "TBL",
			PlaysToday: true, GamesNext7Days: 3,
			Last7:  SkaterStats{G: 3, A: 5, PlusMinus: 4, PIM: 2, PPP: 4, SOG: 28},
			Season: SkaterStats{G: 12, A: 22, PlusMinus: 8, PIM: 14, PPP: 15, SOG: 95},
		}, {
			Player: "Anthony Stolarz", ID: 8476932,
			NHLPosition: "G", Slot: SlotG, Team: "TOR",
			PlaysToday: false, GamesNext7Days: 3,
			Last7:  GoalieStats{W: 1, GA: 8, GAA: 2.67},
			Season: GoalieStats{W: 5, GA: 30, GAA: 2.50},
		}},
		YourNotes: "punting PIM, climbing PPP",
		YourAnalysis: DailyAnalysis{
			StrongCategories: []string{"G (rank 1)", "SOG (rank 2)"},
			WeakCategories:   []string{"GA (rank 5)", "W (rank 4)"},
			OpenRosterSpots:  0,
			RosterFull:       true,
		},
	}

	got, err := BuildDailyPrompt(input)
	require.NoError(t, err)

	// Spot-check the special-character category keys round-trip
	// untouched. JSON struct tags allow "+/-" — pin that behavior.
	assert.Contains(t, got, `"+/-"`, "+/- must appear as a literal JSON key")
	assert.Contains(t, got, `"GAA"`)
	assert.Contains(t, got, `"is_you":true`)

	// Re-parse to confirm structural validity.
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &raw))
	assert.Equal(t, "2025-11-15", raw["day"])

	// Heterogeneous Last7/Season (skater vs goalie) must marshal
	// differently within the same array. Verify by re-parsing the
	// roster and checking the goalie row carries goalie-only fields.
	roster, ok := raw["your_roster"].([]any)
	require.True(t, ok)
	require.Len(t, roster, 2)
	goalie := roster[1].(map[string]any)
	last7 := goalie["last_7"].(map[string]any)
	_, hasW := last7["W"]
	_, hasG := last7["G"]
	assert.True(t, hasW, "goalie last_7 must include W")
	assert.False(t, hasG, "goalie last_7 must NOT include G (that's a skater field)")
}

// Compact (non-indented) output is intentional — see the BuildDailyPrompt
// comment. Pin "no leading whitespace inside object" so a refactor can't
// silently switch to MarshalIndent and pay 10% more tokens per call.
func TestBuildDailyPrompt_IsCompactJSON(t *testing.T) {
	got, err := BuildDailyPrompt(DailyPromptInput{Day: "x"})
	require.NoError(t, err)
	assert.False(t, strings.Contains(got, "\n  "),
		"daily prompt must be compact JSON; indentation costs ~10%% in tokens on every (non-cacheable) turn")
}

// ============================================================================
// Draft prompt
// ============================================================================

func TestBuildDraftPrompt_RoundTrips(t *testing.T) {
	input := DraftPromptInput{
		DraftInfo: DraftInfo{
			Round: 3, Pick: 27, OverallPick: 27,
			TotalPicks: 90, NextPickIn: 8, SnakeDirection: "ascending",
		},
		YourRoster: []DraftRosterRow{
			{Player: "Nathan MacKinnon", ID: 8477492, Position: "C", Slot: "C", Pick: 1},
		},
		SlotsRemaining: map[string]int{"C": 1, "LW": 2, "RW": 2, "D": 2, "G": 2, "Util": 1, "BN": 6},
		AvailableByPosition: map[string][]DraftablePlayer{
			"C": {{Player: "Centre 1", ID: 1, Position: "C", LastSeason: SkaterStats{G: 30, A: 50}}},
			"G": {{Player: "Goalie 1", ID: 9, Position: "G", LastSeason: GoalieStats{W: 35, GA: 100, GAA: 2.50}}},
		},
		BestAvailableOverall: []DraftablePlayer{
			{Player: "Centre 1", ID: 1, Position: "C", LastSeason: SkaterStats{G: 30, A: 50}},
		},
	}

	got, err := BuildDraftPrompt(input)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &raw))

	info := raw["draft_info"].(map[string]any)
	assert.Equal(t, "ascending", info["snake_direction"])
	assert.EqualValues(t, 3, info["round"])
}

// ============================================================================
// Free-agent ranking
// ============================================================================

func TestRankFreeAgentSkaters_DescendingByComposite(t *testing.T) {
	input := []FreeAgentSkaterCandidate{
		{Player: "low", ID: 1, Last7: SkaterStats{G: 1, A: 1, PPP: 0}},    // composite 2
		{Player: "high", ID: 2, Last7: SkaterStats{G: 4, A: 3, PPP: 2}},   // composite 9
		{Player: "middle", ID: 3, Last7: SkaterStats{G: 2, A: 2, PPP: 1}}, // composite 5
	}
	got := RankFreeAgentSkaters(input, 3)
	require.Len(t, got, 3)
	assert.Equal(t, "high", got[0].Player)
	assert.Equal(t, 9, got[0].RecentScore)
	assert.Equal(t, "middle", got[1].Player)
	assert.Equal(t, "low", got[2].Player)
}

// Ties on composite must break by ID ascending — deterministic and
// independent of input order. Pin this so a quicksort change doesn't
// reorder ties.
func TestRankFreeAgentSkaters_TieBreaksByID(t *testing.T) {
	input := []FreeAgentSkaterCandidate{
		{Player: "p3", ID: 3, Last7: SkaterStats{G: 2, A: 2, PPP: 1}}, // composite 5
		{Player: "p1", ID: 1, Last7: SkaterStats{G: 2, A: 2, PPP: 1}}, // composite 5 (tie)
		{Player: "p2", ID: 2, Last7: SkaterStats{G: 2, A: 2, PPP: 1}}, // composite 5 (tie)
	}
	got := RankFreeAgentSkaters(input, 3)
	require.Len(t, got, 3)
	assert.Equal(t, int64(1), got[0].ID)
	assert.Equal(t, int64(2), got[1].ID)
	assert.Equal(t, int64(3), got[2].ID)
}

// Skater composite is G + A + PPP only — neither +/-, PIM, nor SOG.
// This is a HARD spec invariant: the FA ranking must not weigh
// secondary categories the same as the headline ones. If a future edit
// to skaterCompositeScore adds SOG, this test fails loudly.
func TestRankFreeAgentSkaters_CompositeIgnoresSecondaryCategories(t *testing.T) {
	high := FreeAgentSkaterCandidate{Player: "high", ID: 1, Last7: SkaterStats{G: 5}}               // composite 5
	noisy := FreeAgentSkaterCandidate{Player: "noisy", ID: 2, Last7: SkaterStats{PIM: 99, SOG: 99}} // composite 0
	got := RankFreeAgentSkaters([]FreeAgentSkaterCandidate{noisy, high}, 2)
	require.Len(t, got, 2)
	assert.Equal(t, "high", got[0].Player, "FA ranking must use G+A+PPP only — SOG/PIM must not boost rank")
}

func TestRankFreeAgentSkaters_RespectsTopN(t *testing.T) {
	input := []FreeAgentSkaterCandidate{
		{ID: 1, Last7: SkaterStats{G: 5}},
		{ID: 2, Last7: SkaterStats{G: 4}},
		{ID: 3, Last7: SkaterStats{G: 3}},
	}
	got := RankFreeAgentSkaters(input, 2)
	assert.Len(t, got, 2)
}

func TestRankFreeAgentSkaters_TopNGreaterThanInputClamps(t *testing.T) {
	input := []FreeAgentSkaterCandidate{{ID: 1, Last7: SkaterStats{G: 5}}}
	got := RankFreeAgentSkaters(input, 100)
	assert.Len(t, got, 1)
}

func TestRankFreeAgentSkaters_EmptyInputs(t *testing.T) {
	assert.Nil(t, RankFreeAgentSkaters(nil, 5))
	assert.Nil(t, RankFreeAgentSkaters([]FreeAgentSkaterCandidate{{ID: 1}}, 0))
	assert.Nil(t, RankFreeAgentSkaters([]FreeAgentSkaterCandidate{{ID: 1}}, -3))
}

// Input slice must NOT be mutated — caller may reuse it. Pin the
// invariant since defensive-copy was an explicit design choice.
func TestRankFreeAgentSkaters_DoesNotMutateInput(t *testing.T) {
	input := []FreeAgentSkaterCandidate{
		{Player: "a", ID: 1, Last7: SkaterStats{G: 1}}, // composite 1
		{Player: "b", ID: 2, Last7: SkaterStats{G: 5}}, // composite 5
	}
	originalScores := []int{input[0].RecentScore, input[1].RecentScore}
	originalOrder := []string{input[0].Player, input[1].Player}

	_ = RankFreeAgentSkaters(input, 2)

	assert.Equal(t, originalScores, []int{input[0].RecentScore, input[1].RecentScore},
		"input RecentScore must not be filled in")
	assert.Equal(t, originalOrder, []string{input[0].Player, input[1].Player},
		"input order must be preserved")
}

func TestRankFreeAgentGoalies_DescendingByW(t *testing.T) {
	input := []FreeAgentGoalieCandidate{
		{Player: "g1", ID: 1, Last7: GoalieStats{W: 1}},
		{Player: "g2", ID: 2, Last7: GoalieStats{W: 3}},
		{Player: "g3", ID: 3, Last7: GoalieStats{W: 2}},
	}
	got := RankFreeAgentGoalies(input, 3)
	require.Len(t, got, 3)
	assert.Equal(t, "g2", got[0].Player)
	assert.Equal(t, 3, got[0].RecentScore)
	assert.Equal(t, "g3", got[1].Player)
	assert.Equal(t, "g1", got[2].Player)
}

// Ties on W → lower GA wins. PLAN.md spec: "tie-break by lower GA".
func TestRankFreeAgentGoalies_TieOnW_BreaksByLowerGA(t *testing.T) {
	input := []FreeAgentGoalieCandidate{
		{Player: "leaky", ID: 1, Last7: GoalieStats{W: 3, GA: 12}},
		{Player: "stingy", ID: 2, Last7: GoalieStats{W: 3, GA: 6}},
	}
	got := RankFreeAgentGoalies(input, 2)
	require.Len(t, got, 2)
	assert.Equal(t, "stingy", got[0].Player, "lower GA wins on a W tie")
}

// Tied on W and GA → stable tie-break by ID ascending. Without this,
// daily prompts would jitter on goalies with identical recent
// stat lines.
func TestRankFreeAgentGoalies_TieOnWandGA_BreaksByID(t *testing.T) {
	input := []FreeAgentGoalieCandidate{
		{Player: "g3", ID: 3, Last7: GoalieStats{W: 2, GA: 5}},
		{Player: "g1", ID: 1, Last7: GoalieStats{W: 2, GA: 5}},
		{Player: "g2", ID: 2, Last7: GoalieStats{W: 2, GA: 5}},
	}
	got := RankFreeAgentGoalies(input, 3)
	assert.Equal(t, int64(1), got[0].ID)
	assert.Equal(t, int64(2), got[1].ID)
	assert.Equal(t, int64(3), got[2].ID)
}

// Spec sizes: PLAN.md mandates 20 skaters / 5 goalies in the daily
// prompt. Pin these so a future tuning change is explicit.
func TestMaxFreeAgentSizes(t *testing.T) {
	assert.Equal(t, 20, MaxTopFreeAgentSkaters)
	assert.Equal(t, 5, MaxTopFreeAgentGoalies)
}
