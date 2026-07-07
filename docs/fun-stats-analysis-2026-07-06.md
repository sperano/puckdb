# Analysis: Fun Stats We Could Derive (But Don't)

**Date:** 2026-07-06
**Status:** Analysis / backlog
**Scope:** `database/migrations/` (views), `graph/`, `mcpserver/`, `worker/simulation/`

## Where we stand

The raw data is far richer than what we expose. Current derivation surface is
thin:

- **7 views** (`database/migrations/000007_views.up.sql`): skater/goalie
  season + recent-30-day aggregates, Yahoo roto standings. Only
  `goalie_season_stats` (gaa, sv_pct) and `yahoo_roto_standings` (RANK()
  windows) compute anything non-trivial.
- **GraphQL** exposes raw rows per entity (boxscore, game logs, season totals,
  Edge passthrough). No leaderboards, no rates, no streaks, no head-to-head.
- **MCP (Maurice)** has 55 tools, almost all row-fetch passthroughs. Only 2
  touch `play_events` (raw dump); zero touch `shifts`; zero touch `sim_*`.
- **Sim** exposes standings/transactions via GraphQL and `sim status`/`sim
  tail` CLI — the rich LLM telemetry (tokens, cost, latency, tool outcomes)
  has no query surface at all.

Meanwhile the untapped raw material:

| Table | Rows | What's in it |
|-------|------|--------------|
| `play_events` | ~7.8M | 16 event types, x/y coordinates, `situation_code` (on-ice strength), shot_type, penalty details, faceoff winners/losers, hits, giveaways/takeaways, running score |
| `shifts` | ~14.5M | per-shift start/end/duration per player per period |
| `sim_agent_turns` + tool_calls | grows per sim | tokens, cache hits, cost, latency, tool-call outcomes, full reasoning text |
| Edge tables | per season | speed, distance, shot speed, zone time — passthrough only, never cross-referenced |

**Known blocker:** `play_events` has 14+ player-id columns and indexes only on
`type_desc_key` and `(game_id, period)` (`docs/improvements.md:53-69`).
Player-centric queries are full scans. Any plan below that aggregates
play-by-play per player needs either targeted partial indexes or (better)
materialized rollups built once per import.

---

## Tier 1 — NHL "fun stats" from play_events (highest fun-per-effort)

All derivable today with SQL; propose as **materialized views refreshed after
`ImportSeason`** (not live views — 7.8M-row scans) plus GraphQL/MCP surfaces.

1. **Shot maps & poor-man's xG.** `x_coord/y_coord` + `shot_type` +
   outcome (goal / shot-on-goal / missed / blocked). Bucket the ice into
   zones, compute league shooting % per (zone, shot_type) → every shot gets an
   expected-goal value → **xG for/against per player, per team, goals above
   expected** (deadliest shooters, luckiest teams, goalies stealing games).
   This single stat unlocks half of modern hockey talk.
2. **Corsi/Fenwick (shot-attempt shares)** from shot-on-goal + missed-shot +
   blocked-shot + failed-shot-attempt, split by strength via `situation_code`
   (e.g. 1551 = 5v5). Team and on-ice player versions (on-ice needs shifts
   join, see Tier 2).
3. **Strength-state splits.** `situation_code` encodes goalies+skaters per
   side; nothing uses it. PP/PK/EV goal rates per team, PP unit efficiency,
   empty-net goal hunters, 6-on-5 chaos stats.
4. **Penalty personality.** `committed_by_player_id` vs `drawn_by_player_id`:
   penalties drawn minus taken (agitator index), penalty-type profiles
   (who gets caught hooking vs fighting), team discipline by period,
   refs' calling patterns via `game_officials` join (fun and spicy).
5. **Faceoff geography.** `winning/losing_player_id` + `zone_code` + x/y:
   faceoff win % by player by zone, clutch faceoffs (defensive zone, last 2
   min, one-goal game via `time_remaining` + score columns).
6. **Clutch & chaos.** Running `away_score/home_score` per event enables:
   comeback probability (biggest blown leads), lead-change counts (most
   entertaining games/teams), last-minute goals, "stress index" per fanbase
   (time spent within one goal).
7. **Assist networks.** `scoring/assist1/assist2_player_id` triples →
   who-feeds-whom graphs, most-connected duos/trios across seasons,
   "telepathy score" for linemates.
8. **Hit ledger.** `hitting_player_id`/`hittee_player_id`: biggest hitters,
   most-hit players, hit rivalry pairs, hits-per-60.

## Tier 2 — shifts-derived (nothing exists today; medium effort)

The 14.5M-row `shifts` table is completely unexposed.

1. **Linemate inference.** Overlapping shift windows per (game, team) →
   who actually plays with whom, line combinations over time, chemistry
   (on-ice goal share per inferred pair/trio, joining play_events by
   timestamp). Flagship fun stat: *real* lines vs official rosters.
2. **Fatigue & deployment.** Shift-length distributions, TOI by period,
   short-bench detection (coach shortens to 3 lines in close 3rd periods),
   back-to-back game fatigue curves (join `games.game_date`).
3. **Ironman/leaning stats.** Longest shifts of the season (usually a story:
   stuck on ice during a penalty kill), most shifts per game, quickest
   turnaround between shifts.
4. Joined with play_events timestamps: **on-ice for/against** (who was on the
   ice for goals for/against — the foundation for on-ice Corsi and WOWY
   "with or without you" comparisons).

Prereq: `shifts` lacks a `team_id` index (`docs/improvements.md:61`); the
linemate computation wants `(game_id, team_id, period)`.

## Tier 3 — sim analytics (the most novel; nobody else has this data)

The sim telemetry (`sim_agent_turns`, `sim_agent_tool_calls`,
`sim_transactions`, `sim_standings`) supports **LLM-manager analytics** — stats
about the AIs themselves. All cheap SQL; main work is surfacing (GraphQL +
`sim report` CLI + Grafana):

1. **Cost-per-roto-point.** `sim_agent_turns.cost_usd` vs
   `sim_standings.roto_points`: is claude-sonnet worth 30× llama3.1:8b?
   The headline chart for the whole project.
2. **Decision quality / regret.** For every add/drop in `sim_transactions`,
   join the player's subsequent `game_skater_stats`: points gained by pickups
   vs points forfeited by drops → **waiver-wire IQ per model**. Same for
   draft picks: pick number vs season production = draft value curve per
   model.
3. **Tool-call discipline.** `sim_agent_tool_calls.outcome`
   (accepted / parse_error / validation_rejected / unknown_tool):
   hallucination and error rates per model, recovery rate within a turn
   (`recovered_name`), rounds-per-decision. A model-capability benchmark that
   falls out of the schema for free.
4. **Strategy adherence.** `sim_agents.strategy` is a prompt; did the
   goalie-focused agent actually roster goalies? Measure roster composition
   and category ranks against the stated strategy — quantified
   instruction-following.
5. **Manager personality profiles.** Transactions per week, roster churn,
   panic index (drops after a bad week), loyalty (days held per player),
   pass rate (days doing nothing).
6. **Reasoning archive.** `reasoning` on every transaction + `final_text` per
   turn: a "best/worst calls of the season" digest — the accepted reasoning
   next to what actually happened. Prime material for Maurice to narrate.
7. Once trades ship (see `sim-trades-plan-2026-07-06.md`): trade win/loss
   ledger — post-trade production differential per side, fleece-of-the-season
   award.

## Tier 4 — cross-domain novelties

1. **Edge × boxscore correlations.** Does top skating speed predict points?
   Distance-per-game vs plus-minus. Edge data is passthrough today; one
   join away from leaderboards ("fastest skaters who can't score").
2. **Three-stars bias.** `game_three_stars` vs actual game stats: who gets
   stars without earning them (home-team bias measurable).
3. **Shootout book.** `shootout_attempts` has shot_type and result per
   attempt: shooter tendencies, goalie shootout records, "never picks glove
   side" scouting reports.
4. **Milestones & streaks.** Point streaks, goalie shutout streaks,
   approaching-milestone alerts (career goal 300 in ~3 games) from
   `game_skater_stats` + `player_season_totals`.

---

## Recommended delivery plan

| Phase | Work | Why first |
|-------|------|-----------|
| 1 | Sim analytics pack: `sim report <pool-id>` CLI + GraphQL telemetry queries (cost-per-point, tool-call outcomes, regret) | Zero schema risk, small tables, most novel output, feeds the distributed-ideas doc's "model tournament" |
| 2 | Play-events rollup migration: materialized views `player_pbp_stats` (faceoffs, hits, penalties drawn/taken, shot attempts by strength) + `shot_events` (denormalized shots with coords) refreshed post-import | Unblocks Tier 1 without indexing the 7.8M-row table for ad-hoc queries |
| 3 | xG model v0 (zone × shot-type league averages) on top of `shot_events`; GraphQL `xgLeaders`, MCP `get_shot_map` | The flagship stat |
| 4 | Shifts pack: linemate inference + on-ice goals (batch job writing `inferred_lines`, `player_on_ice_goals`) | Heaviest compute; a natural fit for a Temporal fan-out workflow (per-season children) |
| 5 | MCP tools for all of the above so Maurice can answer "who's the biggest agitator on Boston?" | Turns every derived table into chat material |

Notes:
- Follow the existing pattern: derivations live in migrations as views
  (`000007_views.up.sql` precedent); heavy ones become tables written by
  import-time activities, mirroring `club_skater_stats`.
- Every materialized rollup should expose a Prometheus freshness gauge via the
  existing collector registry so staleness is visible in Grafana.
- Migrations only via `./puckdb db migrate`.
