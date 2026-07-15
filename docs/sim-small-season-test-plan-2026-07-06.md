# Plan: Small-Season Simulation Integration Test (1 Month, 4 Teams, Ollama on ollama.local)

**Date:** 2026-07-06
**Status:** Implemented 2026-07-09 (all phases, incl. phase 5 startDate/endDate) — Layer B runbook in `docs/sim-livellm-smoke.md`
**Scope:** `worker/simulation/`, `cmd/sim.go` config surface, a new `poolsim-smoke.yaml`

Goal: a repeatable end-to-end proof that a full (small) season simulates
cleanly — team names → draft → ~30 day loop → complete — plus an operator
recipe for running the same scenario against real Ollama models on
`ollama.local` (the canonical hostname for the ollama-host Ollama host —
plain `ollama.local` does not resolve).

## What exists today (baseline facts)

- **Pool size is free.** `num_teams` is derived from `len(input.Agents)`
  (`graph/simulation_helpers.go:641`); 4 agents = 4 teams, already the shape
  of `poolsim01.yaml`.
- **"1 month" is expressible** as `maxSeasonDays: 30`
  (`CreateSimPoolInput.MaxSeasonDays` → `PoolConfig.MaxSeasonDays`, enforced
  at `workflow.go:716`). But the window is always the *first* N days of the
  season — the day loop starts at `seasons.standings_start`
  (`workflow.go:179`). **There is no start-date config.** The old integration
  tests bounded runs by `UPDATE seasons SET standings_start/standings_end` —
  a hack we'll keep for the hermetic test but should eventually replace (see
  Phase 5).
- **No real cluster required.** `integration_workflow_test.go` starts an
  in-process Temporal DevServer via `testsuite.StartDevServer` (or dials
  `PUCKDB_TEST_TEMPORAL_HOSTPORT`), uses `PUCKDB_TEST_PG_URL` for Postgres and
  `PUCKDB_TEST_REDIS_ADDR` (default localhost:6379, DB 15) for the progress
  tracker.
- **Ollama routing already works per agent:** `agent.APIBase` overrides the
  provider base URL (`agent.go:65-67`) — `apiBase:
  http://ollama.local:11434/v1` in YAML is all that's needed. Nothing in
  Go references ollama-host; it's config-only. Ollama models cost $0 in
  `pricing.go` (models not in the table), so `maxLlmCostUsdPerPool` never
  trips — don't assert on cost-cap behavior in this test.
- **Data prerequisites for a real season sim:** `seasons` + `season_teams` +
  `players`; **prior-season** `club_skater_stats`/`club_goalie_stats`
  (draft candidates come from `season - 10001`, `state_activity.go:272`); and
  the sim season's `games` + `game_skater_stats`/`game_goalie_stats` with
  `game_state IN ('OFF','FINAL')` for the window (`collect_activity.go`,
  `ListSimDayGames`).

## Blocker to fix first

**The existing integration harness does not compile against HEAD.**
`integration_test.go` and `integration_workflow_test.go` (both
`//go:build integration`) reference removed fields:
`InsertSimAgentParams.{DraftPosition,Name}`, `AgentConfig.Name`, and
`SimPoolWorkflowInput.AutoAdvance` (pre-`stopAfter` design). Since they're
build-tag gated, `go test ./...` never catches this. All the reusable helpers
(`seedScenario`, `seedGame`, `seedGameSkaterStats`, `setupIntegrationWorker`,
`startTemporalForTest`, `connectRedisForTest`) live in these broken files.

## Design: two test layers

### Layer A — hermetic full-small-season test (CI-able, mocked LLM)

`TestIntegrationSmallSeasonFourTeams` in `worker/simulation/`:
deterministic, no network, no real LLM, no ollama-host dependency. This is the
"ensure a full small season can be simulated" guarantee.

- **Scenario:** 4 agents, 4 synthetic NHL teams, ~30 sim days, a compact
  roster (e.g. 1C/1LW/1RW/2D/1G/2BN = 8 slots, `draftRounds: 8`), synthetic
  schedule of 2 games/day with plausible skater/goalie stat lines seeded via
  the repaired `seedGame`/`seedGameSkaterStats` helpers. Prior-season
  `club_skater_stats`/`club_goalie_stats` seeded for the draft pool
  (enough candidates: roster_size × 4 teams × ~2 safety factor).
- **LLM:** mocked `AgentFactory` (existing `scriptedIntegClient` pattern) with
  scripted behavior that exercises the daily tool surface: set_lineup,
  add/drop mid-month, one contested waiver claim, one validation-rejected
  call (to prove the feedback loop), and passes otherwise.
- **Drive:** insert the pool + agents directly (or call
  `createSimPoolImpl`), run `SimPoolWorkflow` on a DevServer-backed worker,
  `run.Get(ctx)` to completion. `stopAfter: never` + `maxSeasonDays: 30`.
  This crosses the `ContinueAsNewDayThreshold = 30` boundary — deliberately
  set the window to 31+ days so the test also proves ContinueAsNew
  rehydration (progress tracker from Redis, day counter continuity).
- **Assertions (end state):**
  - `sim_pools.status = 'complete'`; day count = maxSeasonDays.
  - Every agent has a team name, a full legal roster
    (`UNIQUE (pool_id, player_id)` implicitly proven), and standings rows for
    every scoring day.
  - `sim_agent_totals` per category equals a Go-side recomputation from the
    seeded game stats over the rostered windows (attribution correctness —
    the add/drop mid-month makes this meaningful).
  - `sim_standings` roto points sum per day = expected total (N teams ×
    categories invariant, with tie-splitting).
  - Waiver contest resolved by priority; loser claim `lost`.
  - `sim_transactions` audit trail contains exactly the scripted actions +
    `daily_turn_done` markers; no `error` rows.
- **Runtime target:** < 2 min. Gate: `//go:build integration` +
  `PUCKDB_TEST_PG_URL` skip, same as today.

### Layer B — live-model smoke against ollama.local (operator-run, not CI)

`TestLiveSmallSeasonOllama` (new build tag `livellm`, additionally gated on
`PUCKDB_TEST_OLLAMA_URL`): same seeded scenario but the **real**
`AgentFactory`/`NewAgent` path, 4 agents `provider: ollama` pointing at
ollama-host. This exercises what the mock can't: OpenAI-compatible client against
Ollama, tool-call parsing from a small model, timeout handling, validation
feedback loops with genuinely erratic output.

- Keep it short: `maxSeasonDays: 7`, tiny rosters, `timeoutSeconds` generous
  (small models are slow; the LLM activity timeout is 5 min,
  `llmActivityOptions`).
- Assertions are **liveness, not quality**: pool completes, no turn ends in
  `error` status that isn't a modeled skip, every day has a turn per agent,
  tool-call `outcome` distribution recorded in telemetry. Do NOT assert on
  specific rosters/decisions — llama output is nondeterministic.
- Precondition check in-test: ping `{OLLAMA_URL}/api/tags` and skip with a
  clear message if unreachable or the model is missing. (Verified 2026-07-07:
  `ollama.local:11434` serves llama3.1:8b, llama3.3, qwen2.5:7b/14b,
  qwen3:8b/14b/32b and Ollama listens on all interfaces — but **pfSense blocks
  the cluster subnet 192.0.2.10/24 from initiating into the 192.0.2.10/24
  LAN** (one-way; LAN→cluster works). A pfSense pass rule for
  `192.0.2.10/24 → 192.0.2.10:11434/tcp` is required before
  cluster-side sims can use Ollama agents. Tests run from a LAN machine are
  unaffected.)
- Companion artifact: `poolsim-smoke.yaml` checked in at repo root — the same
  4-Ollama-agent config usable manually via `puckdb sim create --config
  poolsim-smoke.yaml` against the real cluster (uses the actual 20242025
  data already in prod, `maxSeasonDays: 30`).

## Phases

| Phase | Work |
|-------|------|
| 1 | **Repair the harness.** Fix `integration_test.go` / `integration_workflow_test.go` to compile against HEAD (drop `Name`/`DraftPosition`/`AutoAdvance`, adopt `stopAfter`). Add a CI-visible guard: a `go vet -tags=integration ./worker/simulation/` step (or `go build -tags=integration ./...`) in the test skill so tag-gated files can't rot silently again |
| 2 | **Extract seed helpers** into `integration_helpers_test.go`: parameterized `seedSmallSeason(t, cfg)` building N teams / D days / G games-per-day with deterministic stat lines (seeded rand). Reuse `seedGame`/`seedGameSkaterStats` internals |
| 3 | **Layer A test** as specified, including the ContinueAsNew crossing and the attribution recomputation assert |
| 4 | **Layer B test** + `poolsim-smoke.yaml` + a `docs/` runbook section: env vars, expected runtime, how to `sim tail` it while running |
| 5 (opt) | **Add `startDate`/`endDate` to `CreateSimPoolInput`** (nullable, defaulting to season bounds) so "simulate March 2025" is config, not an `UPDATE seasons` hack. Plumb through `PoolConfig` and the day-loop init (`workflow.go:179`). This also makes Layer B able to pick a dense schedule window |
| 6 | Wire Layer A into the `puckdb-test` skill / CI recipe (needs dockerized Postgres+Redis; Temporal comes free via DevServer) |

## Risks / notes

- **DevServer download:** `testsuite.StartDevServer` fetches the Temporal CLI
  on first run (~50MB). Cache it in CI; allow `PUCKDB_TEST_TEMPORAL_HOSTPORT`
  to point at the docker-compose Temporal instead.
- **Draft starvation:** if seeded prior-season candidates < total roster
  slots, the draft stalls into error paths — the seed helper must compute the
  candidate count from the config, not hardcode it.
- **Redis DB 15 hygiene:** the harness flushes DB 15; assert the address
  isn't production Valkey before flushing (existing pattern — keep it).
- **Layer B flakiness is a feature, contained:** it never runs in CI; it's a
  pre-deploy smoke an operator runs when touching the LLM/agent-loop code.
  Failures should dump the `sim_agent_turns`/`sim_agent_tool_calls` rows for
  the failing turn (helper `dumpTurnTelemetry(t, poolID)`).
