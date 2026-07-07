package simulation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// systemPromptTemplate is the exact text from PLAN.md > "System Prompt".
// Two placeholders — {strategy}, {N}. We use plain string substitution
// rather than text/template so this prompt is one `git blame` away
// from the spec and a casual reader doesn't have to learn template
// syntax to verify it matches.
//
// No {name} placeholder: the agent's identity is its strategy, not a
// self-referential label. (Pre-migration 000021 this prompt opened
// with "You are <name>, an AI fantasy hockey manager…" — the agent's
// team_name only exists after Phase 0 PickTeamName completes, and
// referencing it here would invalidate Anthropic's prompt cache once
// the name is picked.)
//
// IMPORTANT: this string MUST be byte-stable across every call for a
// given (agent, pool). Anthropic's prompt cache invalidates on the
// slightest change. Don't add today's date, the day count, or the
// current standings here — those go in the daily/draft context message
// (the non-cacheable suffix).
const systemPromptTemplate = `You are an AI fantasy hockey manager in a {N}-team rotisserie pool.
Your strategy: {strategy}

Rules:
- 9 roto categories: G, A, +/-, PIM, PPP, SOG, W, GA (lower=better), GAA (lower=better)
- Roster: 2C, 2LW, 2RW, 3D, 1Util, 2G active | 6BN bench | 3IR
- Only active players who appear in a game's boxscore accumulate stats
- Players can fill slots matching their NHL position (C, LW, RW, D, or G), or Util (any skater), or BN/IR
- Goalies can only fill G, BN, or IR slots (no Util)
- Active slots are NOT required to be filled. You may leave any active slot empty (or bench every player) as a punting strategy — empty slots simply earn 0 in their categories that day. No minimum-active-slot rule is enforced.

You will be shown the current game state each day. You may adjust your lineup,
add/drop free agents, or do nothing if you're satisfied. If you have no changes
to make, just explain your reasoning — no tool calls needed.

Every action tool (draft_player, set_lineup, add_player, claim_player, drop_player) requires
a "reason" argument: one short sentence (≤200 chars) explaining the move. The reason is
logged per-action so a human can later understand your in-the-moment thinking.

Tool calls are processed in order: adds/drops/claims first, then lineup changes, then notes.
Lineup moves within set_lineup are applied in array order (not atomically).
If you move a player to an occupied slot, the displaced player goes to bench automatically.

Free agents (never owned or cleared waivers) can be picked up instantly with add_player.
Recently dropped players are on waivers — use claim_player to file a claim. Claims are
resolved after the waiver period; highest waiver priority wins contested claims.
Dropped players go on waivers and become free agents if unclaimed.
Your open claims are listed in the daily context under "your_pending_claims" — do not
file another claim for a player you already have a pending claim on.

You have a persistent notes field that carries over between days. Use update_notes to
record strategic observations, plans, or reminders for yourself. Your current notes
(if any) are shown in the context under "your_notes". Keep notes under 50,000 bytes
(roughly 50,000 ASCII characters) — the entire field is included in every daily prompt,
so verbose notes increase your input cost on every call. Treat it as scratchpad, not
journal: prefer terse, current strategy over long history.`

// BuildSystemPrompt fills the system-prompt template for a single agent.
// The result is byte-stable for fixed inputs, which is what makes the
// Anthropic prompt cache hit on every call past the first.
//
// numTeams comes from PoolConfig.NumTeams. agent.Strategy comes from
// AgentConfig. Empty strategy is allowed — the line just reads
// "Your strategy:" with nothing after, which is harmless.
func BuildSystemPrompt(agent AgentConfig, numTeams int) string {
	r := strings.NewReplacer(
		"{strategy}", agent.Strategy,
		"{N}", fmt.Sprintf("%d", numTeams),
	)
	return r.Replace(systemPromptTemplate)
}

// ============================================================================
// Daily prompt — the non-cacheable suffix sent on every per-day turn.
// ============================================================================

// CategoryStanding is the {value, rank} pair the LLM sees per category
// per agent. Value is `any` because counting categories use int and
// rate categories (GAA) use float; the LLM happily reads both forms.
type CategoryStanding struct {
	Value any     `json:"value"`
	Rank  float64 `json:"rank"`
}

// StandingRow is one row of the daily standings table. Field order is
// fixed: agent, is_you, the 6 skater cats, the 3 goalie cats,
// total_roto_pts. The LLM doesn't care about order, but stable order
// keeps prompt diffs across days reviewable.
type StandingRow struct {
	Agent        string           `json:"agent"`
	IsYou        bool             `json:"is_you"`
	G            CategoryStanding `json:"G"`
	A            CategoryStanding `json:"A"`
	PlusMinus    CategoryStanding `json:"+/-"`
	PIM          CategoryStanding `json:"PIM"`
	PPP          CategoryStanding `json:"PPP"`
	SOG          CategoryStanding `json:"SOG"`
	W            CategoryStanding `json:"W"`
	GA           CategoryStanding `json:"GA"`
	GAA          CategoryStanding `json:"GAA"`
	TotalRotoPts float64          `json:"total_roto_pts"`
}

// SkaterStats and GoalieStats are the recurring per-window stat blocks
// used in roster rows and free-agent rows. Categories match the 6/3
// roto split.
type SkaterStats struct {
	G         int `json:"G"`
	A         int `json:"A"`
	PlusMinus int `json:"+/-"`
	PIM       int `json:"PIM"`
	PPP       int `json:"PPP"`
	SOG       int `json:"SOG"`
}

type GoalieStats struct {
	W   int     `json:"W"`
	GA  int     `json:"GA"`
	GAA float64 `json:"GAA"`
}

// RosterRow describes one player on the agent's roster. Last7 and
// Season are `any` so the JSON output naturally renders SkaterStats vs
// GoalieStats based on what the activity stuffed in. Activities are
// expected to put a SkaterStats in for skaters and a GoalieStats in for
// goalies; mixing is invalid and would confuse the LLM.
type RosterRow struct {
	Player         string     `json:"player"`
	ID             int64      `json:"id"`
	NHLPosition    string     `json:"nhl_position"`
	Slot           RosterSlot `json:"slot"`
	Team           string     `json:"team"`
	PlaysToday     bool       `json:"plays_today"`
	GamesNext7Days int        `json:"games_next_7_days"`
	Last7          any        `json:"last_7"`
	Season         any        `json:"season"`
}

// ScheduleGameRow is one entry in todays_schedule. The LLM uses
// goals_per_game as a coarse offense/defense rating so it can decide
// whether to start a goalie facing a weak offense.
type ScheduleGameRow struct {
	Game             string  `json:"game"`
	HomeGoalsPerGame float64 `json:"home_goals_per_game"`
	AwayGoalsPerGame float64 `json:"away_goals_per_game"`
}

// FreeAgentSkaterCandidate / FreeAgentGoalieCandidate carry everything
// the LLM sees per FA row. RecentScore is populated by RankFreeAgentXxx
// before the prompt is built — callers must NOT set it manually.
type FreeAgentSkaterCandidate struct {
	Player         string      `json:"player"`
	ID             int64       `json:"id"`
	NHLPosition    string      `json:"nhl_position"`
	Team           string      `json:"team"`
	PlaysToday     bool        `json:"plays_today"`
	GamesNext7Days int         `json:"games_next_7_days"`
	Last7          SkaterStats `json:"last_7"`
	Season         SkaterStats `json:"season"`
	RecentScore    int         `json:"recent_score"`
}

type FreeAgentGoalieCandidate struct {
	Player         string      `json:"player"`
	ID             int64       `json:"id"`
	NHLPosition    string      `json:"nhl_position"`
	Team           string      `json:"team"`
	PlaysToday     bool        `json:"plays_today"`
	GamesNext7Days int         `json:"games_next_7_days"`
	Last7          GoalieStats `json:"last_7"`
	Season         GoalieStats `json:"season"`
	RecentScore    int         `json:"recent_score"`
}

// TopFreeAgents holds the two pre-ranked sub-arrays the LLM sees.
// Skaters and goalies are kept separate so the LLM can scan positional
// needs without filtering.
type TopFreeAgents struct {
	Skaters []FreeAgentSkaterCandidate `json:"skaters"`
	Goalies []FreeAgentGoalieCandidate `json:"goalies"`
}

// DailyAnalysis is the summary the activity pre-computes for the LLM —
// strong/weak categories, bench-vs-active mismatches, open-spot count.
// The LLM could derive these itself but spending tokens on derivation
// is wasteful when one Postgres query gives the same answer.
type DailyAnalysis struct {
	StrongCategories             []string `json:"strong_categories"`
	WeakCategories               []string `json:"weak_categories"`
	BenchPlayersPlayingToday     []string `json:"bench_players_playing_today"`
	ActivePlayersNotPlayingToday []string `json:"active_players_not_playing_today"`
	OpenRosterSpots              int      `json:"open_roster_spots"`
	RosterFull                   bool     `json:"roster_full"`
}

// PendingClaimRow is one open waiver claim the agent has already
// filed. Shown so the agent doesn't file a duplicate claim — a second
// claim_player on the same player is rejected as an action error.
type PendingClaimRow struct {
	Player     string `json:"player"`
	ID         int64  `json:"id"`
	ResolvesOn string `json:"resolves_on"`
	DropPlayer string `json:"drop_player,omitempty"`
}

// DailyPromptInput is the full set of fields rendered into the
// per-day context message. Keep field order matching the JSON example
// in PLAN.md > "What the Agent Sees" so the rendered prompt and the
// spec stay visually aligned.
type DailyPromptInput struct {
	Day            string            `json:"day"`
	Standings      []StandingRow     `json:"standings"`
	YourRoster     []RosterRow       `json:"your_roster"`
	TodaysSchedule []ScheduleGameRow `json:"todays_schedule"`
	TopFreeAgents  TopFreeAgents     `json:"top_free_agents"`
	PendingClaims  []PendingClaimRow `json:"your_pending_claims"`
	YourNotes      string            `json:"your_notes"`
	YourAnalysis   DailyAnalysis     `json:"your_analysis"`
}

// BuildDailyPrompt renders the daily context as a compact JSON string.
// Compact (not indented) on purpose: this is the NON-cacheable suffix,
// paid for in tokens on every call. Indented JSON adds ~10% to the
// suffix size with no benefit to the LLM. For human review, pipe the
// log line through `jq`.
func BuildDailyPrompt(input DailyPromptInput) (string, error) {
	b, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("simulation: marshal daily prompt: %w", err)
	}
	return string(b), nil
}

// ============================================================================
// Draft prompt — non-cacheable suffix sent on every draft pick.
// ============================================================================

// DraftInfo is the metadata block at the top of the draft context.
// SnakeDirection is "ascending" on odd rounds (1, 3, 5...) and
// "descending" on even rounds — the agent uses this to predict its
// next pick number.
type DraftInfo struct {
	Round          int    `json:"round"`
	Pick           int    `json:"pick"`
	OverallPick    int    `json:"overall_pick"`
	TotalPicks     int    `json:"total_picks"`
	NextPickIn     int    `json:"next_pick_in"`
	SnakeDirection string `json:"snake_direction"`
}

// DraftRosterRow is the lighter roster view shown during drafting —
// just position/slot/pick, no recent stats (no games have been played).
type DraftRosterRow struct {
	Player   string `json:"player"`
	ID       int64  `json:"id"`
	Position string `json:"position"`
	Slot     string `json:"slot"`
	Pick     int    `json:"pick"`
}

// DraftablePlayer is one row in available_by_position / best_available.
// LastSeason carries the prior-season stats — V1 ranks by prior season
// only to avoid leaking current-season stats into draft decisions
// (PLAN.md "Draft ranking uses prior-season stats only").
type DraftablePlayer struct {
	Player     string `json:"player"`
	ID         int64  `json:"id"`
	Position   string `json:"position"`
	LastSeason any    `json:"last_season"` // SkaterStats or GoalieStats
}

// DraftPromptInput is the full draft-pick context.
//
// AvailableByPosition keys are NHL position codes ("C", "LW", "RW",
// "D", "G"); top 10 per unfilled position. BestAvailableOverall is the
// top 5 BPA regardless of position. Both are pre-sorted by the
// activity, so this struct has no ranking responsibility — it's just a
// transport.
type DraftPromptInput struct {
	DraftInfo            DraftInfo                    `json:"draft_info"`
	YourRoster           []DraftRosterRow             `json:"your_roster"`
	SlotsRemaining       map[string]int               `json:"slots_remaining"`
	AvailableByPosition  map[string][]DraftablePlayer `json:"available_by_position"`
	BestAvailableOverall []DraftablePlayer            `json:"best_available_overall"`
	// Notes carries the agent's persistent scratchpad — sim_agents.notes.
	// Updated via update_notes during the draft (multi-round) and across
	// drafts via the daily phase. The agent sees its own running notes
	// on every draft turn so cross-pick planning works ("locked elite
	// C in round 1, target G in 5-6"). Empty until the agent writes.
	Notes string `json:"notes"`
}

// BuildDraftPrompt renders the draft context as compact JSON. Same
// no-indent rationale as BuildDailyPrompt.
func BuildDraftPrompt(input DraftPromptInput) (string, error) {
	b, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("simulation: marshal draft prompt: %w", err)
	}
	return string(b), nil
}

// ============================================================================
// Free-agent ranking — pure functions, no IO.
// ============================================================================

// MaxTopFreeAgentSkaters / MaxTopFreeAgentGoalies are the per-PLAN.md
// sizes of the two sub-arrays in `top_free_agents`. The full FA pool
// can be 700+ players mid-season; truncating to these counts keeps the
// daily prompt under control while still showing enough breadth for
// positional planning.
const (
	MaxTopFreeAgentSkaters = 20
	MaxTopFreeAgentGoalies = 5
)

// skaterCompositeScore computes the ranking key for a free-agent
// skater: G + A + PPP over the last_7 window. PLAN.md > "Free-agent
// ranking" > "Skater composite". Other categories (+/-, PIM, SOG) are
// intentionally excluded — they're either neutral (PIM is dual-edged)
// or already correlated with G/A (SOG).
func skaterCompositeScore(s SkaterStats) int {
	return s.G + s.A + s.PPP
}

// RankFreeAgentSkaters returns the top-N skater FAs by composite,
// descending. Ties are broken by ID ascending — deterministic and
// independent of input order. RecentScore is set on the returned
// elements; the input slice is not mutated.
//
// topN <= 0 returns an empty slice. topN > len(input) is clamped to
// len(input).
func RankFreeAgentSkaters(candidates []FreeAgentSkaterCandidate, topN int) []FreeAgentSkaterCandidate {
	if topN <= 0 || len(candidates) == 0 {
		return nil
	}
	scored := make([]FreeAgentSkaterCandidate, len(candidates))
	copy(scored, candidates)
	for i := range scored {
		scored[i].RecentScore = skaterCompositeScore(scored[i].Last7)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].RecentScore != scored[j].RecentScore {
			return scored[i].RecentScore > scored[j].RecentScore
		}
		return scored[i].ID < scored[j].ID
	})
	if topN > len(scored) {
		topN = len(scored)
	}
	return scored[:topN]
}

// RankFreeAgentGoalies returns the top-N goalie FAs by W desc, with a
// tie-break of lower GA (so a 3W/8GA goalie ranks above 3W/12GA). PLAN
// keeps the goalie composite simple because the FA goalie pool is
// typically thin and recent-W is the primary signal. RecentScore is
// set to W on the output rows.
func RankFreeAgentGoalies(candidates []FreeAgentGoalieCandidate, topN int) []FreeAgentGoalieCandidate {
	if topN <= 0 || len(candidates) == 0 {
		return nil
	}
	scored := make([]FreeAgentGoalieCandidate, len(candidates))
	copy(scored, candidates)
	for i := range scored {
		scored[i].RecentScore = scored[i].Last7.W
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Last7.W != scored[j].Last7.W {
			return scored[i].Last7.W > scored[j].Last7.W
		}
		// W tie → lower GA wins
		if scored[i].Last7.GA != scored[j].Last7.GA {
			return scored[i].Last7.GA < scored[j].Last7.GA
		}
		// Both tied on W and GA → stable ID order for determinism
		return scored[i].ID < scored[j].ID
	})
	if topN > len(scored) {
		topN = len(scored)
	}
	return scored[:topN]
}
