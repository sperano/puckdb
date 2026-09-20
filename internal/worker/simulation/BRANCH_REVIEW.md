# Branch Review — `feature/simulation`

A reading guide for the simulation feature branch. Ordered to follow the dependency graph: foundations first, then layers that build on them. Each section calls out what to scrutinize and links to the relevant commits / files on GitHub.

**Branch:** [`feature/simulation`](https://github.com/sperano/puckdb/tree/feature/simulation)
**Total commits since `main`:** 63 (≈30 planning + ≈33 implementation)
**Net code change:** ~17,000 lines added across the simulation, llm, graph, metrics, and cmd packages.

---

## How to read this

Each section names a small set of files + commits to look at. The suggested order matches dependencies (you need `simulation/types.go` before reading any activity), so reading top-to-bottom should let you understand each piece without forward references. Skim sections marked **(skim)**; focus on the **(focus)** ones — those are where the architecturally interesting decisions live.

If you only have an hour: read the **(focus)** sections plus the workflow (Phase 3.2). Two hours: add the activities (Phase 3.1). Beyond that, the GraphQL surface and CLI are smaller and self-contained — read them last.

---

## Scope summary

Six phases, all complete:
- **Phase 1**: foundation (DB, types, queries, scoring)
- **Phase 2**: LLM agent (prompts, tools, recovery, draft logic, pricing)
- **Phase 3**: workflow + activities + metrics + tests
- **Phase 4**: GraphQL + CLI
- **Production-gating follow-ups** + **V2 backlog** at the end

Result: an end-to-end runnable hockey-pool simulator. `puckdb sim create --config pool.json` → `puckdb sim run <pool-id>` → `puckdb sim status <pool-id>`. Workflow drives the snake draft, then the per-day loop calls 5 LLM-driven and DB activities; cost-capped at the LLM-spend cap; idempotent against Temporal retries; ContinueAsNew every 30 days.

---

## Pre-implementation: planning rounds **(skim)**

The first ~28 commits on the branch are revisions to [`PLAN.md`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/PLAN.md) and [`CHECKLIST.md`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/CHECKLIST.md) in response to a critical review (commit [`e3530c0`](https://github.com/sperano/puckdb/commit/e3530c0)). The plan was iterated through about two dozen design fixes before any code landed — covering everything from "5 strict positions, no F mapping" to "cost cap with auto-pause" to "drain signals before ContinueAsNew."

You don't need to read those commits individually. Just open the current `PLAN.md` and `CHECKLIST.md` — they're the consolidated end state. Notable design decisions that come up repeatedly in the code:
- **Determinism discipline** (PLAN.md > "Determinism") — workflow goroutines must be deterministic for replay.
- **Cost cap** (PLAN.md > "Cost cap > Enforcement") — pre-flight check before every LLM call.
- **Idempotency** (PLAN.md > "Idempotency") — every LLM activity has a probe that skips re-billing on retry.
- **Signal interaction matrix** (PLAN.md > "Signal interaction matrix") — `paused`/`autoAdvance`/`pendingAdvances` state machine.

Open these and skim the "Determinism", "Idempotency", and "Cost cap" sections before reading the workflow. The rest you can reference as needed.

---

## Phase 1 — Foundation

### Database migration

**Commit:** [`421e70a`](https://github.com/sperano/puckdb/commit/421e70a) — "add 000013_simulation migration for sim_* tables"

Twelve tables. The shapes worth noticing:

- **[`000013_simulation.up.sql`](https://github.com/sperano/puckdb/blob/feature/simulation/database/migrations/000013_simulation.up.sql)** — `sim_pools.workflow_id` is a `GENERATED ALWAYS AS ('sim-pool-' || id::text) STORED` column. Eliminates the race window where the workflow_id could be assigned independently of the pool's PK. Most other interesting choices (FK to players(id), partial indexes on active slots, CHECK on notes ≤ 50KB) are direct consequences of bullets in PLAN.md > "Database schema."

**What to look at first:** `sim_transactions` — the audit log table. The "per-type column population" pattern (typed columns instead of a JSONB blob) is a real maintainability call: every transaction kind populates a different subset of columns, but the schema stays queryable. Compare with `yahoo_transactions` (which uses JSONB) for context.

### Go types and slot eligibility

**Commit:** [`f3c5ff2`](https://github.com/sperano/puckdb/commit/f3c5ff2) — "add Go types for pool/agent config and slot eligibility"

**File:** [`worker/simulation/types.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/types.go)

The 5-position table (C/LW/RW/D/G; **no F mapping**) is the operational shape. `EligibleSlots` returns an error for sqlcdb's `PlayerPositionF` (a valid Postgres enum value) rather than silently filtering — PLAN.md > "Position Eligibility" calls for loud rejection over silent drops.

Also note: `AgentConfig.Temperature` is `*float64` so a 0.0 value (deterministic sampling) is distinguishable from "unset."

### sqlc queries

**Commit:** [`5ba0a15`](https://github.com/sperano/puckdb/commit/5ba0a15) — "add sqlc queries for sim_* tables"

**File:** [`sqlcdb/queries/sim.sql`](https://github.com/sperano/puckdb/blob/feature/simulation/sqlcdb/queries/sim.sql) (530 lines) → generated [`sqlcdb/sim.sql.go`](https://github.com/sperano/puckdb/blob/feature/simulation/sqlcdb/sim.sql.go)

**Skim** the queries themselves — they're routine inserts/lists/upserts. **Focus** on these three:

1. **`ListSimFreeAgentCandidates`** — UNION of `game_skater_stats` and `game_goalie_stats` joined with `games`, EXCEPT existing pool rosters / pending claims / recently-dropped players. The "free agent" definition lives entirely in this query.
2. **`ListSimTransactions`** — uses 0/NULL sentinels for optional filters so a single query handles four call sites (workflow's `log` query, GraphQL resolver, audit, no-filter dump).
3. **`AggregateSimAgentDailyStats`** — INSERT...SELECT with `NULLIF(SUM(COALESCE(...)))` for goalie GA components. The NULLIF is what keeps all-skater days from writing spurious zero-component goalie rows.

### Roto scoring engine **(focus)**

**Commit:** [`fb4b15c`](https://github.com/sperano/puckdb/commit/fb4b15c) — "add Yahoo-style roto scoring engine"

**Files:**
- [`worker/simulation/scoring.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/scoring.go) (~200 lines)
- [`worker/simulation/scoring_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/scoring_test.go) (~365 lines, mostly worked examples)

The Yahoo tie-splitting formula `each_tied_agent_gets = N + 1 - P - (K-1)/2` is the heart of this file. The tests pin every worked example from PLAN.md > "Tie-breaking" — including the GAA category's zero-TOI worst-rank rule (uses +Inf as a sentinel so tied-bottom agents still split fractional points correctly). Also worth noting: `AgentRotoTotals` sorts desc with `AgentID` ascending tiebreak — important because workflow replay must produce the same standings every time.

---

## Phase 2 — Agent

### `llm` package extensions **(focus)**

**Commits:**
- [`74d97db`](https://github.com/sperano/puckdb/commit/74d97db) — "add prompt-caching support and agentloop helper"
- [`686f355`](https://github.com/sperano/puckdb/commit/686f355) — "functional options on client constructors"

**Files:**
- [`llm/types.go`](https://github.com/sperano/puckdb/blob/feature/simulation/llm/types.go) — `Cacheable bool` on `Message` and `Tool`
- [`llm/anthropic.go`](https://github.com/sperano/puckdb/blob/feature/simulation/llm/anthropic.go) — translates `Cacheable` to `cache_control: {"type": "ephemeral"}` on the LAST cacheable block in wire order
- [`llm/agentloop/agentloop.go`](https://github.com/sperano/puckdb/blob/feature/simulation/llm/agentloop/agentloop.go) — extracted from Maurice's chat loop; DB-/MCP-/persistence-agnostic

The `Cacheable` placement rule (precedence: message > tool > system; only the last cacheable block carries the marker) is the subtle bit. A single breakpoint suffices for the system+tools cached-prefix pattern this codebase uses, so Anthropic's prompt cache lights up on call 2+ if the agent's system prompt + tools stay byte-stable. The `Cacheable bool` field opts in per-block; non-Anthropic clients silently ignore it.

`agentloop.Run` has a clean termination contract (PLAN.md > "Phase 0c"): no tool calls = success; executor error = abort; max rounds = success with the trailing tool-requesting response; caller decides whether to coerce a final reply.

### Prompts and free-agent ranking

**Commits:**
- [`1630d4f`](https://github.com/sperano/puckdb/commit/1630d4f) — "system, draft, daily prompts and free-agent ranking"
- [`bdafe76`](https://github.com/sperano/puckdb/commit/bdafe76) — "tool definitions for draft and daily phases"

**Files:**
- [`worker/simulation/prompts.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/prompts.go) (~360 lines)
- [`worker/simulation/tools.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/tools.go) (~250 lines)

The prompts are JSON-rendered (no indentation) for token efficiency. `RankFreeAgentSkaters` / `RankFreeAgentGoalies` are pure functions with defensive copies — they're called from V2 polish work that's currently unwired (CHECKLIST.md > "Known V2 Follow-ups"). The exported size constants (`MaxTopFreeAgentSkaters` / `MaxTopFreeAgentGoalies`) keep the spec sizes in one place.

### Agent struct + tool-call parser

**Commit:** [`1f92390`](https://github.com/sperano/puckdb/commit/1f92390) — "Agent struct, factory, and tool-call parser"

**File:** [`worker/simulation/agent.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/agent.go)

Agent caches the system prompt + tool list at construction so prompt-caching markers stay byte-stable across calls. `ParseAction` is a clean type-switch over the 6 tool args. The `instrumentedLLMClient` wrapper (added later in Phase 3.3, [`7fdb9c7`](https://github.com/sperano/puckdb/commit/7fdb9c7)) sits around `Agent.Client` to observe Prometheus metrics — every cached agent automatically gets metrics for free.

### Fuzzy recovery **(focus)**

**Commit:** [`b21e45b`](https://github.com/sperano/puckdb/commit/b21e45b) — "fuzzy recovery for malformed tool calls"

**Files:**
- [`worker/simulation/recovery.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/recovery.go)
- [`worker/simulation/recovery_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/recovery_test.go)

Three layers of recovery for LLM-misformatted tool calls:
1. **Levenshtein-distance tool-name matching** (max distance 2) — handles typos like `draft_payer`, `drop_palyer`, `set_linup`
2. **Lenient JSON** (state-machine cleanup that preserves string contents) — handles trailing commas, unquoted keys
3. **Phase-scope enforcement** — `RecoverAction` is gated on the legal tool list, so a wrong-phase tool gets rejected even on exact match

The 6 tool names in the codebase are pairwise edit distance ≥ 3 — pinned in tests so a future tool addition can't accidentally land within fuzzy-match range of an existing one (would silently misroute).

### Tool-execution validators

**Commit:** [`4b62e2f`](https://github.com/sperano/puckdb/commit/4b62e2f) — "tool-execution validators"

**File:** [`worker/simulation/validate.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/validate.go)

`ValidateAndResolveLineup` is the most subtle one — it implements the displacement rule (lowest-player_id displaced to BN) on a defensive copy of the placements map. Sentinel errors (`ErrPlayerNotFreeAgent`, `ErrRosterFullNeedsDrop`, etc.) so activities can branch via `errors.Is` rather than string matching.

### Draft logic

**Commit:** [`5c029af`](https://github.com/sperano/puckdb/commit/5c029af) — "draft logic — snake order, ranking, fallback picker"

**File:** [`worker/simulation/draft.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/draft.go)

`SnakeDraftOrder` and `FallbackDraftPick` are both pure-function. The fallback picks the highest-deficit position (ties broken by fixed C/LW/RW/D/G order so retries land on the same player) — used when the LLM has failed twice on a draft turn. The "Util-rostered players count toward their NHL position" detail in `positionFillCounts` is a small but important rule.

### Pricing

**Commit:** [`3707d8c`](https://github.com/sperano/puckdb/commit/3707d8c) — "pricing table and per-call cost helper"

**File:** [`worker/simulation/pricing.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/pricing.go)

Hardcoded per-(provider, model) pricing table. Documented as "reviewed quarterly" — accepted risk that stale prices silently bill instead of pause. The cache-read discount is significant for Anthropic models (cache_read ~ 10% of input rate) and the table tracks it explicitly.

---

## Phase 3.1 — Activities

This is the meat of the runtime layer. Read in order:

### Activity skeleton + first two activities

**Commit:** [`3f7e736`](https://github.com/sperano/puckdb/commit/3f7e736) — "activity layer skeleton + BuildFreeAgentPool"
**Commit:** [`67c4051`](https://github.com/sperano/puckdb/commit/67c4051) — "UpdateStandingsActivity wraps the scoring engine"

**File:** [`worker/simulation/activities.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/activities.go)

The `Activities` struct grows with each new activity. Initial shape: just `Queries SimQueries`. By the end of Phase 3.1 it has `Queries`, `Tx Transactor`, `Signaler WorkflowSignaler`, `ProviderConfigs`, `AgentFactory`, mutex-guarded `agentCache`. The `SimQueries` interface is narrowed (not `*sqlcdb.Queries`) so tests can inject a mock that implements only the methods their activity touches.

`BuildFreeAgentPool` and `UpdateStandings` are the simple read-only activities. Read these two first; they set the testing pattern (`testsuite.TestActivityEnvironment` + `stubSimQueries`).

### DraftPickActivity **(focus)**

**Commit:** [`3663a39`](https://github.com/sperano/puckdb/commit/3663a39) — "DraftPickActivity with idempotency, cost-cap, fallback"

**Files:**
- [`worker/simulation/draft_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/draft_activity.go) (~390 lines)
- [`worker/simulation/transactor.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/transactor.go) (~70 lines)
- [`worker/simulation/signaler.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/signaler.go) (~45 lines)
- [`worker/simulation/draft_activity_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/draft_activity_test.go) (~540 lines, 12 test cases)

The first LLM-driven activity, plus the shared infrastructure (`Transactor`, `WorkflowSignaler`, agent cache) both LLM activities use. **What to look at:**

- The 6-step flow: idempotency probe → cost-cap probe → 1 LLM attempt → 1 retry → fallback → atomic commit. Each step has a clear exit condition.
- `Transactor.InTx` callback receives a `SimQueries` handle scoped to the open `pgx.Tx`. `*sqlcdb.Queries` structurally satisfies `SimQueries` (the interface was assembled from sqlc's signatures), so `sqlcdb.New(tx)` is passed directly without an adapter type.
- `agentCache` keyed by `(pool_id, agent_id)` so prompt-caching markers stay byte-stable.
- Cost-cap branch's asymmetric design: DB writes atomic in one tx, signal goes out AFTER tx commits. If signal fails, retry observes cost-cap-tripped again, re-signals (workflow's pause handler is idempotent).

### ManageRosterActivity **(focus)**

**Commit:** [`3d44f06`](https://github.com/sperano/puckdb/commit/3d44f06) — "ManageRosterActivity for the daily turn"

**Files:**
- [`worker/simulation/manage_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/manage_activity.go) (~700 lines)
- [`worker/simulation/costcap.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/costcap.go) — extracted from draft_activity.go in this commit
- [`worker/simulation/manage_activity_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/manage_activity_test.go) (~700 lines, 22 test cases)

The most complex activity. Uses `agentloop.Run` for multi-tool conversations (drop + add + lineup + notes in one turn). The tool executor closure mutates a working copy of the roster + FA pool so subsequent tool calls validate against post-prior-actions state.

Key design: **executor never returns an error** (would abort the loop and lose every prior accepted action). Validation rejections are formatted as `"error: <reason>"` strings fed back to the LLM as tool results, letting the model self-correct on the next round.

The cost-cap helpers (`checkCostCap`, `runCostCapBranch`) are extracted to `costcap.go` here since both LLM activities share them.

### CollectDayStatsActivity

**Commit:** [`1d7dfca`](https://github.com/sperano/puckdb/commit/1d7dfca) — "scores rosters against NHL stats"

**Files:**
- [`worker/simulation/collect_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/collect_activity.go)
- [`worker/simulation/collect_activity_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/collect_activity_test.go) (13 cases)

No LLM. Per-game batch fetch outside the tx, per-agent rollup inside. **Goalie decision semantics** (PLAN.md > "Goalie decision semantics" — only `decision='W'` counts toward W; L/OTL/T/NULL all → 0; GA accumulates from EVERY row including NULL-decision) are pinned in three test cases.

### ProcessWaiversActivity

**Commit:** [`6efc992`](https://github.com/sperano/puckdb/commit/6efc992) — "resolves claims by priority"

**Files:**
- [`worker/simulation/waivers_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/waivers_activity.go)
- [`worker/simulation/waivers_activity_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/waivers_activity_test.go) (16 cases)

Pure DB. `resolveClaims` is a pure function — group by player_id, sort by priority asc, lowest-priority-number wins. `demoteWinners` rebuilds priority order with non-winners compacted up + winners appended (preserving relative pre-resolution order, dedup'd if an agent won multiple claims). Both helpers are independently testable without spinning up the activity environment.

---

## Phase 3.2 — Workflow **(focus)**

**Commit:** [`3150550`](https://github.com/sperano/puckdb/commit/3150550) — "SimPoolWorkflow ties activities together"

**Files:**
- [`worker/simulation/workflow.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/workflow.go) (~580 lines)
- [`worker/simulation/state_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/state_activity.go) (~350 lines, extended further in production-gating commit)
- [`worker/simulation/workflow_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/workflow_test.go) (12 test cases)

**This is the architectural keystone.** Read in this order:

1. The phase outline at the top — Phase 1 (Draft) → Phase 2 (Season day loop) → Phase 3 (Complete).
2. `SimPoolWorkflow` function — the top-level orchestrator. Note the selector dispatcher pattern for signal handling.
3. `shouldProcessNow` — pure-function go/no-go check. Pulled out so the signal interaction matrix is unit-testable; the matrix tests pin the canonical behavior (including the subtle "advance is valid in any state" rule that overrides paused).
4. `runSeasonPhase` day loop — drain protocol before ContinueAsNew, ctx.Err() check at the top of every iteration.
5. `shuffleAgents` — uses `workflow.SideEffect` to capture a seed once per draft (recorded in event history → replay-safe). PLAN.md mentions a `workflow.NewRandom` helper but that doesn't exist in the SDK; SideEffect is the canonical primitive.

The `LoadPoolStateActivity` and `LoadDraftCandidatesActivity` (in `state_activity.go`) are read-only boot-up activities; the workflow calls them once on entry and once after each ContinueAsNew.

---

## Phase 3.3 — Prometheus metrics

**Commit:** [`7fdb9c7`](https://github.com/sperano/puckdb/commit/7fdb9c7) — "Prometheus metrics for LLM cost + day pacing"

**Files:**
- [`metrics/metrics.go`](https://github.com/sperano/puckdb/blob/feature/simulation/metrics/metrics.go) — three new metrics + helpers
- [`worker/simulation/instrumented_client.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/instrumented_client.go) — `llm.Client` decorator
- [`worker/simulation/telemetry_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/telemetry_activity.go) — workflow→Prometheus bridge
- [`worker/simulation/instrumented_client_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/instrumented_client_test.go)

The decorator pattern around `llm.Client` is the design choice worth noticing: by wrapping at NewAgent construction time rather than instrumenting each call site, both DraftPickActivity (per-attempt Complete) AND every `agentloop.Run` round inside ManageRosterActivity get instrumentation for free. Zero changes to the activity hot path.

The day-duration metric is the workflow→Prometheus bridge: workflow goroutines can't observe Prometheus directly (non-deterministic side effect breaks replay), so the workflow brackets `processOneDay` with `workflow.Now` reads (deterministic), computes the duration, and dispatches `RecordDayDurationActivity` which performs the histogram observation.

---

## Phase 4.1 — GraphQL schema

**Commit:** [`c200242`](https://github.com/sperano/puckdb/commit/c200242) — "GraphQL schema + gqlgen-regenerated resolver stubs"

**File:** [`graph/simulation.graphqls`](https://github.com/sperano/puckdb/blob/feature/simulation/graph/simulation.graphqls) (~180 hand-written lines)

Lives in its own file (not appended to `schema.graphqls`) so gqlgen's `follow-schema` layout drops the resolver stubs into `simulation.resolvers.go` in isolation from the existing schema/data resolvers.

**The map-input trick**: `RosterPositions` is `[SimRosterPositionInput!]!` (a list of `{slot, count}` records) because GraphQL doesn't have map types. The resolver flattens to `map[RosterSlot]int` at the boundary.

The header comment in the file documents the resolver-to-Temporal-query mapping (which fields are cheap, which are lazy).

---

## Phase 4.2 — GraphQL resolvers

**Commit:** [`78b2041`](https://github.com/sperano/puckdb/commit/78b2041) — "GraphQL resolver implementations"

**Files:**
- [`graph/simulation.resolvers.go`](https://github.com/sperano/puckdb/blob/feature/simulation/graph/simulation.resolvers.go) — gqlgen-managed; thin wrappers
- [`graph/simulation_helpers.go`](https://github.com/sperano/puckdb/blob/feature/simulation/graph/simulation_helpers.go) — hand-written, off the regen path
- [`graph/simulation_resolver_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/graph/simulation_resolver_test.go)

The resolver/helper split mirrors gqlgen's regen contract: bodies in `simulation.resolvers.go` survive a regen, but anything else gets moved to "the end of the file" — so put the meat in a sibling file, leave thin wrappers in the gqlgen-managed one.

`loadSimPool` is the central read-side helper composing `GetSimPool` + `ListSimAgentsByPool` + per-agent `ListSimRosterByAgent` + `GetSimStandingsLatestDate` + `ListSimStandingsByDate`. `assemblePlayerNameMap` batches player name lookups (V1 N+1 over `GetPlayer`; flagged as a V2 follow-up).

`createSimPoolImpl` does the two-step insert+execute flow for `createSimPool`. `signalSimPool` centralizes the signal pattern shared by advance/pause/auto_advance.

---

## Phase 4.3 — CLI commands

**Commit:** [`b88dcff`](https://github.com/sperano/puckdb/commit/b88dcff) — "puckdb sim CLI — six bootstrap/observe commands"

**Files:**
- [`cmd/sim.go`](https://github.com/sperano/puckdb/blob/feature/simulation/cmd/sim.go) (~300 lines)
- [`cmd/sim_client.go`](https://github.com/sperano/puckdb/blob/feature/simulation/cmd/sim_client.go) (~150 lines)
- [`cmd/sim_test.go`](https://github.com/sperano/puckdb/blob/feature/simulation/cmd/sim_test.go) (~25 sub-cases)

Six subcommands: `create`, `advance`, `run`, `pause`, `cancel`, `status`. Notable patterns:

- **`signalCommand` factoring** — four signal commands (advance/run/pause/cancel) collapse into a struct-based factory. Three lines per command instead of forty.
- **`DisallowUnknownFields`** on the JSON decoder — typos in the user's config error immediately rather than silently dropped.
- **GraphQL aliases** in `SimPoolStatus` (`pool: simPool(...)` + `progress: simPoolProgress(...)`) — single round-trip for the dashboard query.
- **`renderSimPoolStatus`** is a pure function over the response (no I/O dependencies) so tests pass an in-memory buffer.

---

## Production-gating follow-ups

**Commit:** [`0f9837f`](https://github.com/sperano/puckdb/commit/0f9837f) — "sim is end-to-end runnable"

Four things this commit lands together:

1. **`SetPoolStatusActivity`** — replaces the workflow's `writePoolStatus` logger stub with a real activity.
2. **`InsertSimAgent` in `createSimPoolImpl`** — the GraphQL `createSimPool` mutation now writes both the pool AND its agents.
3. **`LoadDraftCandidates` real query** ([`worker/simulation/state_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/state_activity.go)) — replaces the V1 stub with `GetClubSkaterStatsBySeason` + `GetClubGoalieStatsBySeason`, aggregating per-player across team rows (handles trades), filtering positions, ranking by score.
4. **`BuildManageRosterContextActivity`** ([`worker/simulation/context_activity.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/context_activity.go)) — replaces the workflow's in-memory `assembleManageRosterInput` helper with a DB-backed activity. Loads agent notes, full roster (with positions), and the latest standings snapshot tagged with `is_you`.

After this commit, the simulation runs end-to-end. `puckdb sim create` writes both pool and agents; the workflow's draft phase sees real ranked candidates; the daily turn passes a real `DailyPromptInput` to the LLM.

---

## V2 backlog

**Commit:** [`8750a7b`](https://github.com/sperano/puckdb/commit/8750a7b) — "consolidate V2 follow-ups into CHECKLIST.md"

**File:** [`worker/simulation/CHECKLIST.md`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/CHECKLIST.md) — new "Known V2 Follow-ups" section

Eight items, grouped by failure mode (prompt context / roster stats / team display / N+1 / GraphQL laziness / CLI gaps / Maurice / workflow polish). The standout for "next easy win" is **`TopFreeAgents`** in the daily prompt: the rankers (`RankFreeAgentSkaters` / `RankFreeAgentGoalies`) are already implemented and tested; only the wire-through to `BuildManageRosterContextActivity` is missing.

---

## Test summary

| Package | Test files | Sub-cases |
|---|---|---|
| `worker/simulation` | 11 | ~150 |
| `graph` | 1 (sim) | ~25 |
| `cmd` | 1 (sim) | ~25 |
| `metrics` | 1 (sim additions) | 4 |
| **Total** | **14** | **~200+** |

Whole-module `go test -race ./...` is clean. The only outstanding test work is **Phase 3.4 integration tests** (under `integration` build tag, not started) — full draft + 5-day run with mock-LLM agents against real Postgres + Temporal.

---

## Suggested review path

If you have:

- **30 min** — Read PLAN.md "Determinism" + "Idempotency" + "Cost cap" sections; skim the workflow ([`workflow.go`](https://github.com/sperano/puckdb/blob/feature/simulation/worker/simulation/workflow.go)).
- **1 hour** — Add `DraftPickActivity` and `ManageRosterActivity` (the two LLM activities — they share infrastructure but differ in agentloop usage).
- **2 hours** — Add Phase 1 foundation (scoring engine, validators) and Phase 4 (resolvers + CLI).
- **Half-day** — Read everything top-to-bottom in the order above. The branch is ~17K lines but ≥30% is tests; the prose-to-code ratio is high in the architecturally interesting sections.

Things the review should specifically scrutinize:
1. The `Transactor` / `WorkflowSignaler` interfaces — clean abstractions or unnecessary indirection?
2. Cost-cap branch ordering (DB tx first, signal after) — is the retry semantics genuinely correct?
3. Tool-executor closure mutating working state across rounds — is the deep-copy on entry sufficient?
4. ContinueAsNew + drain protocol — does the `pendingAdvances` counter survive a CAN cleanly?
5. The map-iteration determinism risk in `applyAction` and `decodeStandings` — already mitigated, but worth a second look.
