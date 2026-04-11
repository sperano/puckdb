x# PuckDB Improvements — Post-Feature-Complete Assessment

**Date:** 2026-04-08
**Context:** Drafted after auditing CLAUDE.md against the actual codebase and `puckdb_prod` schema. The April 2026 cleanup plan (batch helpers, resolver dedup, worker subpackages) is complete, so this document is forward-looking rather than cleanup-oriented.

---

## 1. GraphQL API barely exposes the data

**Finding:** The schema is ~95% workflow control plane (start/cancel/result/progress for 13 workflows). The only *data* query is `processPlayersResultData`. For a project whose point is to *have* NHL + Yahoo data queryable, this is backwards.

**Suggested additions:**
- `player(id: Int!)`, `players(filter)`, `playerGameLog(playerId, season)`
- `team(id)`, `teamRoster(teamId, season)`
- `game(id)`, `gamesByDate(date)`, `boxscore(gameId)`
- `standings(date)`, `standingsRange(from, to)`
- `yahooLeague(key)`, `yahooTeamRoster(teamKey, week)`, `yahooMatchups(leagueKey, week)`
- `playerSeasonTotals(playerId)` — including minor-league rows

**Why it's highest leverage:** This is what unlocks Maurice, unlocks any frontend, and unlocks external consumers. Everything else in this document is marginal until the data has a query surface.

---

## 2. Data integrity gaps

Discovered while verifying schema for the CLAUDE.md audit.

**~~Missing foreign keys~~ — RESOLVED:**
- ~~`standings_snapshots` → no FK~~
- ~~`yahoo_matchups`, `yahoo_transactions`, `yahoo_draft_results` → no FKs~~
- ~~`club_skater_stats`, `club_goalie_stats` → no FKs to `players` / `teams`~~
- ~~`player_season_totals` → no FK (intentional? includes minor leagues)~~
- ~~`player_awards` → no FK~~
- Foreign keys added across all tables. `player_season_totals` intentionally uses `ON DELETE SET NULL` for `player_id` and `team_id` since rows include minor-league data that may not have matching NHL entities.

**~~Inconsistent key naming~~ — RESOLVED:**
- ~~Some tables use `season_id` (FK to `seasons.id`)~~
- ~~`season_rosters` uses `season` (integer, no FK)~~
- ~~`standings_snapshots.team_abbrev` is text, not `team_id`~~
- All tables now use `season` consistently (never `season_id`). `standings_snapshots` uses `team_id` in PK with FK to `season_teams(season, team_id)`. `team_abbrev` retained as a non-PK display column.

**~~Denormalized free text~~ — RESOLVED:**
- ~~`player_season_totals.team_name` is text rather than FK — can't reliably join to `teams`~~
- `player_season_totals` now has `team_id BIGINT` with composite FK `(season, team_id)` → `season_teams(season, team_id)` using `ON DELETE SET NULL (team_id)`. 5 pre-NHL western league teams (PCHA/WCHL Stanley Cup challengers, 1917-1925) seeded into `season_teams` with synthetic IDs 70-74. Non-NHL rows (75% of table) have `team_id = NULL`. All team lookup functions return errors for unknown abbreviations to prevent silent bad data.

---

## 3. `play_events` / `shifts` performance

These are the two largest tables and the most likely source of future slow queries.

**`play_events` (7.8M rows):**
- Has 14+ player_id columns (`scoring_player_id`, `assist1_player_id`, `hitting_player_id`, `blocking_player_id`, etc.)
- **No index on any of them.** A query like "every event involving player X" is a full table scan.

**`shifts` (14.5M rows):**
- Missing index on `team_id`.

**Partitioning:** Both are natural range-partition candidates by `game_id` or by season via `games.season_id`. `pg_partman` is already installed in the cluster.

**Action:**
1. Add indexes on high-cardinality player FK columns in `play_events` (partial indexes where nullable).
2. Benchmark "player career events" and "team shifts for season" queries before/after.
3. Evaluate partitioning once queries are defined (from item 1 above).

---

## 4. Yahoo data model weaknesses

**~~`yahoo_team_summary_stats` is EAV~~ — RESOLVED:**
- ~~Stores `stat_id` / `value` (text) rows rather than typed columns~~
- ~~Queries need to pivot or join repeatedly~~
- ~~Stat IDs are magic numbers with no lookup table~~
- `yahoo_team_summaries` now has 20 typed `REAL` columns from the start. EAV table was never created. Stat name/ID mapping remains in `yahoo_league_stat_categories`.

~~**`yahoo_transactions.players` was JSONB with no GIN index:**~~
- ~~"Every transaction involving player X" is a JSONB scan~~
- Normalized into a dedicated `yahoo_transaction_players` relational table with PK `(league_id, transaction_key, player_id)` and FK cascade. JSONB column dropped entirely.

---

## 5. Operational SLOs

**Observed gaps:**
- No workflow-level SLO metrics (success rate, p95 duration, last-success-timestamp per workflow)
- No data freshness metric like `puckdb_db_latest_game_age_seconds` or `puckdb_standings_lag_seconds`
- Unclear whether a daily automated sync exists — if it does, there's no metric that would page when it stops working

**Action:**
- Add `puckdb_workflow_last_success_timestamp{workflow}` gauge
- Add `puckdb_data_freshness_seconds{dataset}` gauge updated by collector
- Grafana alert on staleness thresholds

---

## 6. Observability gaps

**Current metrics are infrastructure-centric** (HTTP durations, FS ops, download counts) rather than domain-centric.

**Missing:**
- `puckdb_rows_inserted_total{table}` — track ingest volume per table
- `puckdb_rows_updated_total{table}`
- `puckdb_activity_retry_total{activity}` — Temporal retry pressure
- `puckdb_nhl_api_errors_total{endpoint, status}`

**Naming collision:**
- `puckdb_http_request_duration_seconds` is registered in BOTH the Worker registry and the API registry with the same name. Prometheus scrapes them separately (different targets), but the label semantics differ (`api=path` in API vs `api=nhl.com` in Worker). This will confuse dashboards.
- **Rename one.** Suggest `puckdb_api_http_request_duration_seconds` for the API middleware metric.

---

## ~~7. Data dictionary~~ — RESOLVED

~~Many columns have semantic meaning that isn't discoverable from the schema alone.~~

Replaced free-text and integer-coded columns with 15 PostgreSQL `CREATE TYPE ... AS ENUM` types defined natively in the CREATE TABLE statements (migration 000001). Enum types provide self-documenting schemas queryable via `pg_enum`, enforce valid values at the database level, and generate type-safe Go code through sqlc.

**Enums created:** `game_type`, `game_state`, `game_schedule_state`, `period_type`, `play_event_type`, `zone_code`, `ice_side`, `goalie_decision`, `player_position`, `hand_side`, `official_role`, `chat_role`, `shootout_result`, `shift_type`, `shift_detail`.

**Additional changes:**
- `play_events.situation_code` converted from TEXT to INT (was always a numeric string)
- `play_events.type_code` dropped (redundant with `type_desc_key` enum)
- `players.position` and `shoots_catches` empty-string defaults replaced with NULL
- `COMMENT ON COLUMN` added for `situation_code` and `penalty_type_code`

---

## 8. Maurice is nascent

**Current state (from `puckdb_prod`):**
- 8 conversations, 73 messages
- Schema: `maurice_conversations`, `maurice_messages` with `tools_used` text[]
- No feedback column (thumbs-up/down, user corrections)
- No evaluation harness
- No regression fixtures

**Action:**
- Add `maurice_messages.feedback` (enum or smallint)
- Build a golden-set: 20-50 questions with known-good answers, replay nightly
- Log token usage per conversation for cost tracking
- Item 1 (GraphQL data queries) directly enables Maurice to actually answer hockey questions

---

## 9. Other CLAUDE.md files

During the main audit, only `puckdb/CLAUDE.md` was checked. The following likely have similar drift:

- `~/code/workspaces/puckdb/nhl-api-go/CLAUDE.md`
- `~/code/hollingsworth/CLAUDE.md`

**Action:** Audit both against their respective repos — same methodology (walk the actual code and config, diff against documented claims).

---

## Prioritization

| Rank | Item | Leverage | Effort |
|------|------|----------|--------|
| 1 | GraphQL data query layer (#1) | Very high — unlocks everything else | Medium |
| 2 | ~~Missing FKs~~ (done) + `play_events` indexes (#2, #3) | High — correctness + future query performance | Low-medium |
| 3 | Workflow SLO + freshness metrics (#5) | High — production reliability | Low |
| 4 | ~~Data dictionary~~ (done) — PG enums (#7) | Medium — enables Maurice + new contributors | Low |
| 5 | Maurice evaluation harness (#8) | Medium — depends on #1 to be meaningful | Medium |
| 6 | Observability domain metrics (#6) | Medium | Low |
| 7 | ~~Yahoo EAV~~ (done) / ~~JSONB normalization~~ (done) (#4) | Medium | Low |
| 8 | CLAUDE.md audits for sibling repos (#9) | Low | Low |

**Single highest-impact next step:** Start item #1 (GraphQL query layer). It's the thing that converts puckdb from "ingestion pipeline" to "data product."
