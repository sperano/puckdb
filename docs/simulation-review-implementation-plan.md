# Simulation Review Implementation Plan

Implements the findings of [simulation-review-2026-06-09.md](simulation-review-2026-06-09.md)
via phased subagents, sequenced to avoid concurrent edits to shared files
(`workflow.go`, `manage_activity.go`, `validate.go`, `sim.sql`).

## Model assignment

**Recommendation: Opus for Phase 3 only; Sonnet everywhere else.**

Rationale: phase duration is driven by task size (Phase 1 on Opus took ~21 min
for the heaviest cluster; the next Opus agent took ~3 min), so switching models
buys little on the big phases. Phase 3 is the exception — commit-time
revalidation under Temporal retries (converting unique-constraint violations
into clean rejections inside the turn transaction) is subtle multi-file
concurrency work where Opus reduces the risk of a plausible-but-wrong fix.

Agent definitions (in `~/.claude/agents/`):
- `go-expert` — pinned to **sonnet**; routine Go implementation, refactoring, tests.
- `go-genius` — pinned to **opus**; correctness-critical work (retry semantics,
  transaction boundaries, idempotency, concurrency).

## Phases

| Phase | Findings | Agent (model) | Status |
|-------|----------|---------------|--------|
| 1 | #1 game_state filter, #2 idempotency probe, #7 telemetry delete key | go-genius (opus) | ✅ done — migration 000024, `daily_turn_done` marker, sqlc regenerated |
| 2a | #3 cost-cap pause vs complete, #4 MaxSeasonDays across CAN | go-genius (opus) | ✅ done — `runSeasonPhase` returns paused bool; cap uses absolute days from SeasonStartDate |
| 2b | #12 cost accounting (pricing table, date-suffixed IDs, partial usage billed) | go-expert (sonnet) | ✅ done — Opus rows $5/$25, prefix/date-suffix lookup, agentloop returns partial usage on error |
| 3 | #5 cross-day waiver wedge, #6 contested-add retry loop, #11 waiver resolution revalidation | **go-genius (opus)** | ✅ done — player-grouped claim resolution, commit-time revalidation (`commit_rejected` outcome), capacity recheck at resolution |
| 4 | #8 bench↔active swap, #9 added-player position map, #10 inverted standings | go-expert (sonnet) | ✅ done — mover excluded from BN count, benchFull gate for adds, FA positions pre-populated, rank derived by sorting roto_points desc, Value populated, DB errors propagated |
| 5 | Low items: roto-scale totals, parseActionLenient set_team_name, mid-draft cost-cap flood, QuerySummary fields, same-day waiver priority rotation | go-expert (sonnet) | ✅ done — AgentIDs zero-padding in UpdateStandings, lenient set_team_name case, draft loop breaks on cap trip, QuerySummary populated, winner rotates to bottom between contested groups |
| 6 | Full workspace test suite + build verification | main thread | ✅ done — all packages pass (simulation 78.1% coverage, agentloop 96.9%); nhl-api-go green |

## Constraints (all phases)

- Phases run sequentially except 2a ∥ 2b (disjoint files).
- Migrations: create numbered files only; never hand-apply.
- sqlc regeneration (`sqlc generate`, repo root) after any `internal/sqlcdb/queries/` change,
  plus SimQueries interface + test stub updates.
- Named constants, no magic numbers; match existing style.
- Full test pass required before any commit; no commits by agents.
