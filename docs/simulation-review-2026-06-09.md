# Simulation Branch Bug Review — 2026-06-09

Code review of `feature/simulation` (`worker/simulation/` + related sqlc queries and
migrations), focused on the post-draft season loop, which has never been executed
end-to-end. Reviewed by one orchestrator pass over `workflow.go` plus three parallel
review agents covering the activity layer (roster management, waivers/scoring,
recovery/telemetry/pricing). The two critical findings were independently re-verified
against the SQL and the live database.

## Critical — the season produces no results at all

### 1. Daily scoring never runs: `game_state = 'FINAL'` matches almost nothing

- `ListSimDayGames` (`sqlcdb/queries/sim.sql:535`) filters `game_state = 'FINAL'`,
  but completed historical games are stored as `'OFF'`. Verified against the live DB:
  65,237 regular-season games are `'OFF'`, only 4 are `'FINAL'`.
- `game.sql:222,321` already uses the correct `IN ('OFF', 'FINAL')` form.
- Effect: `CollectDayStats` returns "no games" every day, and since `UpdateStandings`
  is gated on it (`workflow.go:785`), the whole season completes with zero stats and
  zero standings.
- Same dead filter at `sim.sql:569` (`GetTeamGoalsPerGame`, currently V2-unused).

### 2. Every agent's day-1 turn is silently skipped — no lineup is ever set on opening day

Found independently by two reviewers.

- ManageRoster's idempotency probe `ExistsSimTransactionForDay`
  (`manage_activity.go:121`, `sim.sql:371-374`) matches **any** transaction type for
  `(pool, agent, date)`.
- Draft picks are stamped `Date = SeasonStartDate` (`workflow.go:583`), and the day
  loop starts at that same date (`workflow.go:170`). On day 1 every agent's own
  `draft_pick` rows trip the probe → `Skipped("already_managed")`.
- Since drafted players all land on BN (`draft_activity.go:432`), every team fields
  an empty lineup on day 1 and scores zero. No error is surfaced.

Two more manifestations of the same untyped probe:

- **Waiver winners lose their daily turn.** `ProcessWaivers` runs first in the day
  loop and inserts `add`/`drop` rows dated sim_date for the winner
  (`waivers_activity.go:226-234, 253-262`); the winner's ManageRoster later that day
  is skipped — the agent that just won a claim can never slot the new player that day.
- `cost_cap_reached` audit rows (also written at `Date = SimDate`, `costcap.go:97-102`)
  trip the probe too, so a pool resumed after raising the cap permanently skips that
  agent/date.

**Fix:** filter the probe by transaction type, or use a dedicated marker type for
"daily turn completed".

## High — wedges and retry loops in unexercised paths

### 3. Cost-cap pause during the season marks the pool `complete`

- The cost-cap exit in `runSeasonPhase` returns `nil` (`workflow.go:683-686`), which
  the caller cannot distinguish from "season ended" — `SimPoolWorkflow` falls through
  to Phase 3 and `completePool` (`workflow.go:181`) overwrites the `'paused'` status
  the cost-cap activity just wrote with `'complete'`.
- The team-name and draft pause paths return without completing; the season path
  needs the same treatment (sentinel error or bool return).

### 4. `MaxSeasonDays >= 30` is silently ignored

- The cap check uses `in.DayCount` (`workflow.go:650`), but ContinueAsNew resets
  `DayCount` to 0 every 30 days (`workflow.go:688-693`,
  `ContinueAsNewDayThreshold = 30`).
- A cap of e.g. 50 never triggers — the counter never gets past 30 — and the sim runs
  to the season end date.
- **Fix:** carry the cap across CAN (decrement it in the CAN input, or track absolute
  days elapsed from `SeasonStartDate`).

### 5. Cross-day duplicate waiver claims permanently wedge the day loop

- `process_date = filed_date + offset` per claim (`manage_activity.go:729-740`), and
  `ProcessWaivers` only groups claims **due today** (`waivers_activity.go:78-95,
  159-190`). Two agents claiming the same player on different days are never in the
  same contested group — the earlier filer wins automatically, bypassing waiver
  priority entirely.
- Nothing cancels the later agent's still-`pending` claim after the player is won
  (`ListSimPlayersOnWaivers` excludes only `status='won'`, `sim.sql:484-500`;
  `ValidateClaimPlayer` checks only `OnWaivers`, `validate.go:110-116`). On the later
  claim's due date it resolves as an uncontested "win" → `InsertSimRoster`
  (`waivers_activity.go:243-251`) hits `UNIQUE (pool_id, player_id)` on `sim_rosters`
  → `ProcessWaivers` fails and Temporal retries forever. The day loop is wedged on a
  constraint violation no retry can clear.
- Related: an agent re-claiming a player it already has a pending claim on (the daily
  prompt has no pending-claims block) violates
  `ux_sim_waiver_claims_pending_one_per_agent_player` and rolls back its entire daily
  turn.

### 6. Same-day contested adds hard-fail the loser's turn and re-bill the LLM on retry

- The free-agent pool is built once per day and shared across all agents
  (`workflow.go:727-769`); `ValidateAddPlayer` (`validate.go:88-104`) validates
  against that stale list and nothing re-checks at commit time.
- When agent #2 in the day's order adds a player agent #1 already took,
  `InsertSimRoster` violates `UNIQUE (pool_id, player_id)` inside `commitDailyTurn`'s
  transaction (`manage_activity.go:697-705`) → the whole turn rolls back (all other
  accepted actions, notes, telemetry) → Temporal retries with the same stale input,
  re-running and re-billing the LLM each attempt.
- The `add_player` tool description promises a clean "first-processed agent wins"
  loss (`tools.go:278`); the actual behavior is a poisoned retry loop. Agents sharing
  identical FA context converging on the same hot pickup makes this near-certain.
- **Fix:** commit-time revalidation that converts the conflict into a clean rejected
  action instead of a transaction failure.

### 7. Draft telemetry: each pick deletes the previous pick's entire trace

- `RecordTurnTelemetry` starts with `DeleteSimAgentTurnIdempotent` keyed on
  `(pool_id, agent_id, phase, sim_date)` (`telemetry.go:232-239`, `sim.sql:587-592`).
- All of an agent's draft picks share that coordinate (phase=`draft`,
  sim_date=season start). Migration 000018 enforces uniqueness on that key;
  `pick_number` exists as a column but is not part of it.
- In an N-round draft, the round-2 pick deletes the round-1 pick's `sim_agent_turns`
  row, CASCADE-wiping its rounds, tool_calls, and message transcripts. Only the last
  pick per agent survives; draft token/cost aggregates undercount by ~draft_rounds×.

## Medium

### 8. Bench↔active swaps rejected once the bench is full

- The displacement check (`validate.go:229-240`) counts the moving player as still
  occupying BN when they are moving *from* BN. A bench↔active swap is BN-neutral, yet
  it is rejected with "BN is full" whenever BN is at its limit — the steady state
  every team reaches after filling its roster. The documented auto-displacement
  mechanic (`prompts.go:49`, `tools.go:98,272`) fails on every attempt.
- Aggravator: `rosterFull` (`validate.go:147-153`) counts IR slots in total capacity
  while adds always land in BN with no per-slot check, so the bench can permanently
  exceed its cap.
- **Fix:** exclude the mover from the BN count when `fromSlot == SlotBN`.

### 9. A player added this turn can't be slotted this turn

- `applyAddToWorkingState` (`manage_activity.go:502-509`) updates placements and
  removes the dropped player's position but never inserts the added player's position
  into `work.positions` (built only from the turn-start roster,
  `context_activity.go:164-189`).
- The documented "round 1: drop/add/claim, round 2: set_lineup" flow
  (`manage_activity.go:94-104`) fails: `catalog.Position(newPlayer)` errors, the
  lineup move is rejected, and every pickup is stuck on BN until the next day.
- Secondary: a roster player with NULL `players.position` is silently omitted from
  the map and becomes permanently un-slottable.

### 10. Standings shown to agents are inverted

- `context_activity.go:244-259` builds `CategoryStanding{Rank: ...}` from
  `sim_standings.roto_points` (confirmed: `UpdateStandings` writes `RotoPoints`,
  `activities.go:384-395`). The field is documented and JSON-rendered as "rank"
  (`prompts.go:82-88`); `Value` is left nil.
- In a 6-team pool the category leader displays `"rank": 6` and the worst team
  `"rank": 1` — inverted. Every agent makes daily decisions from misread standings.
- Bonus: a transient DB error from `GetSimStandingsLatestDate` is swallowed
  (`context_activity.go:208-212`) and rendered as "no standings yet".

### 11. Waiver resolution never re-validates state

- `applyWaiverResolution` (`waivers_activity.go:196-264`) applies the win
  unconditionally. Roster capacity was checked only at filing time; adds between
  filing and `process_date` mean the winner's roster can exceed total capacity at
  resolution — no recheck, no forced drop.
- If the designated `drop_player_id` already left the roster, `DeleteSimRoster`
  silently deletes 0 rows but the `drop` transaction row is still inserted — a
  phantom drop that re-opens the player's waiver window / FA-pool exclusion even if
  they're on another agent's roster, and a later claim win on them hits
  `UNIQUE (pool_id, player_id)` and wedges `ProcessWaivers` (same failure mode
  as #5).
- An agent winning two claims that share one `drop_player_id` logs the drop twice
  and nets +2/−1 roster spots.

### 12. Cost accounting wrong in both directions

- `pricing.go:82-83`: Opus 4.6/4.7 priced at $15/$75 (cache 18.75/1.50); actual
  published pricing is **$5/$25** (cache write $6.25, cache read $0.50). Every Opus
  call is billed 3×, so `total_llm_cost_usd` inflates 3× and the cost cap
  (`costcap.go:56`) pauses the pool at one-third of the real budget.
- `pricing.go:84`: `claude-sonnet-4-7` is not a real model ID (dead row). Sonnet 4.6
  and Haiku 4.5 rows are correct.
- The lookup is exact-match after lowercasing (`pricing.go:104-111`), so real
  date-suffixed IDs (`claude-haiku-4-5-20251001`, `claude-sonnet-4-5-20250929`) bill
  $0 and the cap never trips.
- Tokens from a mid-loop LLM failure are never billed: `agentloop.Run` returns
  `(nil, err)` discarding accumulated usage (`llm/agentloop/agentloop.go:117-119`),
  and `costFromAggregate(cfg, nil)` records $0 (`manage_activity.go:299-302` — the
  comment there claims partial usage is recorded; it isn't). Same pattern at
  `draft_activity.go:347` and `state_activity.go:574`. The per-round usage *is*
  available via the telemetry captures fallback (`telemetry.go:346-362`) but is only
  used for token columns, not cost. A flaky provider retried by Temporal bills the
  operator repeatedly while the cap counter never moves.

## Low

- **Missing totals rows compress the roto scale** — `RecomputeSimAgentTotals*`
  (`sim.sql:203-229`) only emits rows for agents present in `sim_agent_daily_stats`;
  `RankCategory` sizes points off `len(stats)` (`scoring.go:83-126`). An agent whose
  goalies haven't played yet gets zero points in those categories (for GA,
  lower-is-better, it should rank first at 0), and everyone else's points compress —
  cross-day totals aren't comparable.
- **`parseActionLenient` can't recover `set_team_name`** — the lenient-JSON switch
  (`recovery.go:333-374`) omits `ToolSetTeamName` even though `ParseAction` handles
  it; layer-3 recovery always fails during the team-name phase.
- **Mid-draft cost-cap trip floods audit rows/signals** — the draft loop only checks
  pause after all picks (`workflow.go:162`); once the cap trips at pick k, every
  remaining pick independently re-runs `runCostCapBranch`
  (`draft_activity.go:153-157`): one duplicate `cost_cap_reached` row + pause signal
  each (~150 in a 12×14 draft). Those rows also feed finding #2.
- **`QuerySummary` never populates `Status` / `TotalLLMCostUsd`**
  (`workflow.go:286-291`).
- **Same-day contested waiver groups all resolve with pre-resolution priority**
  (`waivers_activity.go:95-121, 153-191`) — under the Yahoo convention the migration
  cites, the priority-1 agent should drop to the bottom after its first successful
  claim, not win every contested player due that day.

## Checked and found clean

- Transactor commit/rollback shape (`transactor.go` — deferred rollback after commit
  is safe per pgx).
- sqlc-generated SQL column names/placeholders vs migrations 000013/000018/000020;
  scan ordering.
- Snake-draft order math, overall-pick arithmetic.
- `RankCategory` tie-split math and `RankGAA` zero-TOI sentinel.
- Telemetry round/sequence zip ordering; capture-closure + defer patterns in the
  executors; `instrumentedLLMClient` (stateless).
- ProcessWaivers retry-after-commit idempotency (status flips in the same tx);
  CollectDayStats/UpdateStandings upsert idempotency.
- Waiver-priority restamping (PK is `(pool_id, agent_id)`, no unique on priority).
- `game_type = 'regular_season'` enum literals; PPP population in the DB.

## Recommended fix order

1. **#1 and #2** — they block any meaningful end-to-end run.
2. **#3–#7** — the wedge/retry-loop class; each can hard-stall a running sim or
   silently corrupt state.
3. **#8–#12** — gameplay-quality and cost-accounting; fix before drawing conclusions
   from sim results.

Two recurring patterns worth keeping in mind while fixing:

- **Idempotency keys that are too coarse** (`(pool, agent, date)` without a type;
  turn-telemetry key without `pick_number`). An over-broad key converts "safe to
  retry" into "silently skips legitimate work."
- **Validate-at-T, commit-at-T+n** (adds validated against a morning FA snapshot,
  waiver claims validated at filing time). Under Temporal, a unique-constraint
  violation inside the commit transaction becomes an infinite retry loop, not a
  graceful rejection — every one of these sites needs commit-time revalidation that
  turns a conflict into a clean "you lost" result.
