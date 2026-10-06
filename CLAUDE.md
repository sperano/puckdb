# PuckDB

Go application that imports NHL hockey data and Yahoo Fantasy Sports data. Downloads from NHL API and Yahoo API, caches locally as files, stores in PostgreSQL. Temporal orchestrates long-running workflows.

## Quick Reference

### Build
```bash
cd /Users/eric/code/puckdb-project/ws/puckdb && go build -o /tmp/puckdb .
```
> **Note:** Always `cd` into the module directory before `go build`. The `-C` flag doesn't work reliably outside a module context.

### Run
```bash
/tmp/puckdb api                      # GraphQL server (port 8787)
/tmp/puckdb worker                   # Temporal worker (port 8788)
/tmp/puckdb metrics                  # Prometheus exporter for cache/Redis/DB
```

### Test
```bash
go test ./...                        # All tests
go test -cover ./...                 # With coverage
```

### Generate Code
```bash
go tool gqlgen generate   # GraphQL (from puckdb root dir; gqlgen is pinned via the go.mod tool directive)
```

**GraphQL code generation:**
1. Edit `internal/graph/schema.graphqls` (schema, types, queries, mutations)
2. Run `go tool gqlgen generate` from puckdb directory
3. Generated files: `internal/graph/generated/generated.go`, `internal/graph/model/models_gen.go`
4. New resolvers appear as `panic("not implemented")` stubs in `internal/graph/schema.resolvers.go`
5. Implement resolver logic in `internal/graph/resolver.go` (private methods like `fetchPlayerLandings`)
6. Update `schema.resolvers.go` stubs to call the new `resolver.go` methods

## CLI Commands

| Command | Description |
|---------|-------------|
| `api` | HTTP server: GraphQL at `/graphql`, playground at `/graphql/`, OAuth at `/yahoo/*` |
| `worker` | Temporal worker for download/import workflows, metrics at `/metrics` |
| `sync` | Sync data into the database |
| `metrics` | Expose cache, Redis, and database metrics as Prometheus metrics |
| `mcp-server` | MCP server exposing curated read-only data tools; HTTP on `--mcp-port` (default 8790) or `--mcp-stdio`; `--mcp-toolsets` picks `nhl` (default), `yahoo` or `nhl,yahoo` (server name `puckdb-<toolsets>`); `--mcp-yahoo-leagues` limits the `yahoo` toolset to listed league keys (empty: every league) |
| `db init` | Create tables, seed NHL data |
| `db drop` | Drop all tables |
| `db migrate` | Run database migrations directly |
| `db force-version <version>` | Clear a dirty migration state after inspecting the schema (see `docs/migration-recovery.md`) |
| `db check-teams` | Report regular-season/playoff games whose team has no `season_teams` row (such games vanish from games queries) |
| `db provision` | Create database/user on shared PostgreSQL |
| `draft rules` | Markdown comparison of leagues' imported rules (scoring, roster, draft, settings) with warnings; `--draft-season`, `--draft-leagues`, `--draft-output` |
| `draft pool` | Coverage of leagues' draftable player pools (eligibility gaps, unmatched NHL players) |
| `draft rankings` | A league's stored ranking snapshot as a table, CSV or JSON (`--draft-league`, `--draft-positions`, `--draft-format`, search/sort/pagination flags); same service and values as GraphQL (see `docs/draft-rankings-api.md`) |
| `sync draft` | Force-refresh and reconcile one full-key Yahoo draft session; `--watch` polls with bounded backoff and final reconciliation (see `docs/yahoo-draft-watch.md`) |
| `draft session` | Inspect live draft state, capability observations, and perform local add/correct/undo/conflict resolution without submitting Yahoo picks |
| `api` → `/draft/` | Maurice live draft board: versioned polling, watch/recovery controls, roster-fit recommendations, shortlist and pick history |
| `news report` | Markdown report of player news: source coverage (fresh/failing/stale/missing), incident candidates with attributed evidence, unattached story subjects; `--news-player-nhl-id`/`--news-player-yahoo-id` for one player (see `docs/draft-player-news.md`) |
| `news events` | Markdown report of validated news events (evidence quotes, lifecycle history) and the extraction review queue (see `docs/draft-news-events.md`) |
| `news eval` | Run the labeled news-event corpus through `--news-extract-provider`/`--news-extract-model`, print accuracy and unsupported-claim rate against the release thresholds, record the run (the gate for automatic effects) |
| `redis flush` | Flush a Redis database |
| `yahoo signout` | Clear OAuth2 token from Redis |
| `yahoo check-access` | Check whether the Yahoo API serves a season's leagues (AUTHORIZED / NOT AUTHORIZED / ERROR), optionally emailing the report over SMTP; meant for a daily CronJob (see `docs/yahoo-access-check.md`) |
| `maurice` | Interactive AI hockey chat REPL |

## Package Structure

Only `main.go` and `cmd/` live at the module root; every library package sits under `internal/`, which the Go toolchain prevents other modules from importing.

| Package | Purpose |
|---------|---------|
| `cmd/` | CLI commands (Cobra + Viper) |
| `internal/worker/` | Temporal workflows and activities, split into `workflow/` (top-level workflows), `nhl/`, `yahoo/`, `player/`, `newsfeed/` (player news refresh), `draftranking/` (draft ranking refresh), `admin/`, `shared/` |
| `internal/graph/` | GraphQL resolvers and schema (gqlgen) |
| `internal/database/` | PostgreSQL connection (pgx), migrations |
| `internal/sqlcdb/` | sqlc-generated type-safe queries |
| `internal/cache/` | File-based caching, XML/JSON parsing |
| `internal/store/` | Storage backends (filesystem, in-memory, instrumented) for raw cached files |
| `internal/resource/` | Typed resource definitions (NHL/Yahoo paths, URLs, parse/format) |
| `internal/core/` | Shared primitives: file types, data origins, time helpers, resource interfaces |
| `internal/config/` | Flags, defaults, seasons YAML parsing |
| `internal/httpx/` | HTTP client, Yahoo API URL builders |
| `internal/metrics/` | Prometheus metrics |
| `internal/temporal/` | Temporal client configuration |
| `internal/matching/` | NHL ↔ Yahoo player matching |
| `internal/draft/` | Draft helper models: normalized league rules, scoring-input validation, roster feasibility, player-pool coverage, comparison reports (see `docs/draft-league-rules.md`) |
| `internal/draftrank/` | Draft ranking service shared by GraphQL, the CLI and exports: refresh (projection → news adjustment → rankings per scenario), immutable per-league snapshots, views (position/search/sort/pagination), explicit issue codes, CSV/JSON/table export (see `docs/draft-rankings-api.md`) |
| `internal/draftrecommend/` | Deterministic live-draft recommendation and replay service over one ranking/session snapshot, with audited numeric reasons and PostgreSQL run storage (see `docs/draft-recommendations.md`) |
| `internal/draftboard/` | Live-board application service: versioned Yahoo session + ranking/recommendation composition, roster feasibility, shortlist and process-scoped watch controls (see `docs/maurice-draft-board.md`) |
| `internal/draftboardui/` | Embedded responsive `/draft/` browser client with keyboard navigation and polling/version recovery |
| `internal/news/` | Player news for the draft helper: source set (`sources.yaml`), RSS/Atom, NHL content and Yahoo status adapters, conditional fetch, article versions, player resolution, incident grouping, coverage and reports (see `docs/draft-player-news.md`) |
| `internal/newsadjust/` | News adjustments for the draft helper: validated event contract, versioned loading of stored extraction events (review and release-gate holds), as-of event selection (dedupe, supersession, returns, rumors), conservative/base/optimistic scenario snapshots, manager overrides, ranking comparison, run storage and replay (see `docs/draft-news-adjustments.md`) |
| `internal/newsevent/` | LLM extraction of validated player news events: prompt and strict output schema, quote/claim/chronology validation, injection defenses, deterministic lifecycle reconciliation (active/superseded/retracted/resolved), labeled evaluation corpus (`evalcorpus.yaml`) and release gate (see `docs/draft-news-events.md`) |
| `internal/yahooaccess/` | Yahoo API access check: game key and league settings probes, AUTHORIZED / NOT AUTHORIZED / ERROR classification, email subject and report (see `docs/yahoo-access-check.md`) |
| `internal/notify/` | Plain-text notification emails over SMTP submission (STARTTLS when offered) |
| `internal/appuser/` | PuckDB users and application sessions: identity from the trusted `X-authentik-uid` header (the proxy must strip client copies), a random `puckdb_session` cookie stored only as an HMAC (`--session-hash-key`), lazy per-request resolution for the GraphQL route (`appuser.Current`); cookie-less callers join the user's active session (30 min idle timeout) |
| `internal/fixtures/yahoofixtures/` | Synthetic Yahoo XML fixtures shared by tests (test-only import) |
| `internal/fixtures/metricsfixtures/` | Reads filesystem operation counts from the worker metrics registry for label assertions (test-only import) |
| `internal/fixtures/draftfixtures/` | Synthetic draft ranking snapshot and in-memory store shared by the draftrank, GraphQL and CLI tests (test-only import) |
| `internal/llm/` | LLM client (used by player enrichment / Maurice) |
| `internal/maurice/` | Prompt + service layer built on top of `internal/llm/` |
| `internal/mcp/` | MCP client (used by Maurice to call tool servers) |
| `internal/mcpserver/` | MCP server exposing curated read-only data tools (`mcp-server` command), split into the `nhl` and `yahoo` toolsets; `league_guard.go` refuses Yahoo leagues outside `--mcp-yahoo-leagues` (refusal = unknown league), and every league-scoped Yahoo tool must go through `leagueGuard.scoped` (enforced by `TestYahooToolsAreLeagueGuarded`) |
| `tls/` | TLS certificates for internal services (gitignored) |

Non-Go directories: `docs/` (design notes, runbooks), `examples/` (sample pool-simulation and gob-cache configs), `scripts/` (operational shell scripts), `.docker/` (compose-only config for Temporal and Grafana provisioning).

### Active Workflows

Defined in `internal/worker/workflow/`:
- `FetchSeasonsWorkflow` / `FetchNHLSeasonWorkflow` / `FetchYahooSeasonWorkflow` — NHL season data (parent + children); the parent also fetches the upcoming (not yet started) season's camp rosters for the prior season's clubs
- `ImportSeasonsWorkflow` / `ImportNHLSeasonWorkflow` / `ImportYahooSeasonWorkflow` — Parse cached files into Postgres; the parent also carries the prior season's clubs forward to the upcoming season (`season_teams`) and imports its camp rosters
- `FetchPlayerLogsWorkflow` / `FetchSeasonPlayerLogsWorkflow` — Per-player game logs
- `ImportPlayerLogsWorkflow` / `ImportSeasonPlayerLogsWorkflow` — Import those logs
- `FetchPlayerLandingsWorkflow` — NHL player landing pages
- `ExtractBoxscorePlayersWorkflow` — Extract player rows from boxscores
- `FetchYahooPlayersWorkflow` — Yahoo player pages
- `ProcessPlayersWorkflow` — Player enrichment and matching
- `FetchEdgeSeasonsWorkflow` / `FetchEdgeWorkflow` — NHL Edge tracking data (2021-2022+)
- `ImportEdgeSeasonsWorkflow` / `ImportEdgeWorkflow` — Import cached Edge data into Postgres
- `RefreshNewsWorkflow` — Fetch due player news sources, store new article versions, resolve players into incident candidates, extract validated events with an LLM when the worker runs with `--news-extract-enabled` (capped calls/tokens per run), prune (on demand, or via the `refresh-news-schedule` Temporal schedule when the worker runs with `--news-schedule-minutes`)
- `RefreshDraftRankingsWorkflow` — Recompute each league's draft ranking snapshot (baseline + news scenarios) one league at a time; a failed league keeps its last snapshot; rejected while running (fixed workflow ID)
- `InitializeWorkflow` — Database initialization

Defined in `internal/worker/admin/`:
- `DropDatabaseWorkflow`, `MigrateDatabaseWorkflow`, `ResetDatabaseWorkflow`, `FlushRedisWorkflow`

Task queue: `puckdb-tasks`

### Progress Tracking Pattern for Child Workflows

When a parent workflow spawns child workflows and displays per-child progress bars, each **child workflow must have its own tracker** so the parent can query it for progress. Without this, progress bars stay at 0.

**Pattern:**

```go
// 1. Create a progress report with Total matching the bar size
func NewFetchEdgeProgressReport(season nhl.SeasonInfo) *shared.ProgressReport {
    total := 192 // must match what parent's Counter function returns
    return &shared.ProgressReport{
        Total: total,
        Groups: []shared.ProgressGroup{
            {Header: fmt.Sprintf("Fetching Edge %s...", season.Label()),
             Bars: []shared.ProgressBar{{Total: total}}},
        },
    }
}

func FetchEdgeWorkflow(ctx workflow.Context, input FetchEdgeWorkflowInput) (core.OriginCounts, error) {
    // 2. Create tracker and register query handler (makes progress queryable)
    tracker, err := shared.InitTracker(ctx, NewFetchEdgeProgressReport(input.Season))
    if err != nil {
        return nil, err
    }
    tracker.StartGroup(ctx, 0)

    // 3. Run activities, incrementing progress as each completes
    for _, f := range futures {
        if err := f.Get(ctx, nil); err != nil {
            return counts, err
        }
        tracker.IncrementBar(ctx, 0, 0) // groupIdx=0, barIdx=0
    }

    // 4. Mark complete when done
    tracker.CompleteGroup(ctx, 0, fmt.Sprintf("Done in %s.", tracker.GetElapsed(ctx, 0)))
    return counts, nil
}
```

**Key points:**
- Parent uses `ChildIDFunc` to set `bar.ChildWorkflowID = "fetch-edge-2024"`
- Parent's progress display queries child workflows by their workflow IDs
- If child has no tracker/query handler registered, query returns empty → bar stays at 0
- Child's progress report Total must match parent's `Counter` function return value
- Return `core.OriginCounts` for aggregation in parent (separate from progress tracking)

**Reference implementations:**
- `FetchNHLSeasonWorkflow` — uses `RunWorkerPool` for automatic tracking
- `FetchEdgeWorkflow` — uses manual `IncrementBar` calls

### Configuration Inside Workflow Code

Workflow code must never read viper flags or config files directly. Any setting that shapes the command sequence (concurrency, batch sizes, which Yahoo leagues to process) is resolved **once at the start of the workflow** through `shared.SnapshotConfig` / `shared.SnapshotConfigInt`, which records the value in history via `SideEffect` so a replay after a worker restart reuses it instead of re-reading changed local settings. ContinueAsNew runs carry the snapshot in their input (see `FetchYahooPlayersInput.Config`). Activity options (timeouts, retry policy) are not part of the determinism check and may stay live. Reference: `internal/worker/workflow/season_config.go`.

## External Services

| Service | Port | Details |
|---------|------|---------|
| PostgreSQL | 5432 | user: `puckdb`, password: `foo`, db: `puckdb` |
| Redis | 6379 | password: `redis` |
| Temporal | 7233 | namespace: `default` |
| Temporal UI | 8080 | Workflow monitoring |

## Data Flow

1. User authenticates via Yahoo OAuth2 (`/yahoo/login` → Redis)
2. GraphQL mutations or CLI trigger Temporal workflows
3. Workers download from NHL/Yahoo APIs → cached files
4. Workers parse cached files → PostgreSQL
5. GraphQL queries serve from PostgreSQL

## Configuration Conventions

Flags follow a strict pattern in `internal/config/`:

1. **Flag names** → constants in `flags.go` (e.g., `FlagAPIPort = "api-port"`)
2. **Defaults** → constants in `defaults.go` (e.g., `DefaultAPIPort = 8787`)
3. **No magic numbers** in flag definitions
4. Each flag has `Init*Flag()` and `Bind*Flag()` functions
5. Cmd files only call these functions, never define flags locally

Season config: `seasons.yaml` (start/end dates, game keys, league IDs, team IDs)

## GraphQL API

Schema lives in `internal/graph/schema.graphqls`. Each long-running workflow follows the same pattern: a `start` mutation, a matching `cancel*` mutation, a `*Result` query, and a `*Progress` query.

**Workflow mutations** (each has a paired `cancel<Name>`):
- `initialize` — Database initialization
- `fetchSeasons(input: SeasonsInput)` — Download NHL season data
- `importSeasons(input: SeasonsInput)` — Parse cached NHL files into Postgres
- `fetchPlayerLogs(input: SeasonsInput)` / `importPlayerLogs(input: SeasonsInput)`
- `fetchPlayerLandings(input: FetchPlayerLandingsInput)`
- `extractBoxscorePlayers(input: SeasonsInput)`
- `fetchYahooPlayers`
- `processPlayers(input: ProcessPlayersInput)` — Player enrichment + matching
- `fetchEdgeStats(input: SeasonsInput)` / `importEdgeStats(input: SeasonsInput)` — Edge tracking data
- `refreshNews(input: RefreshNewsInput)` — Player news refresh (`force`, `sources`, `season`)
- `refreshDraftRankings(input: RefreshDraftRankingsInput)` — Draft ranking snapshots (`season`, `leagueIds`, bench/workload/uncertainty options)

**Admin mutations:** `clearDatabase`, `dropDatabase`, `createDatabase`, `flushRedisDB`

**Maurice (LLM chat) mutations:** `mauriceChat(conversationId, message, idempotencyKey)`, `mauriceDeleteConversation(id)`. Every Maurice field needs an `X-authentik-uid` and is scoped to that user's conversations; resending an `idempotencyKey` returns the original answer (a different prompt under the same key, or a second prompt while a turn runs, is an error)

**Workflow queries:** for every workflow above, `<name>Result: WorkflowResult!` and `<name>Progress: ProgressReport`. `processPlayers` additionally exposes `processPlayersResultData: ProcessPlayersResultData`.

**Data queries** (in `internal/graph/data.graphqls`): `seasons`, `teams`, `players`, `games`, `standings`, `boxscore`, `skaterGameLog`, `goalieGameLog`, `playerSeasonTotals`, `edgeSkaterStats`, `edgeGoalieStats`, `edgeTeamStats`

**Draft rankings** (in `internal/graph/draft.graphqls`, see `docs/draft-rankings-api.md`): `draftLeagues`, `draftRankings`, `draftPlayerComparison`, `draftOverrides`; mutations `createDraftOverride`, `resetDraftOverride`.

**Other queries:** `buildNumber`, `yahooTokenStatus`, `mauriceConversations(limit)`, `mauriceConversation(id)`

## Metrics

The Prometheus registry is split in three (see `internal/metrics/metrics.go`):
- **Worker** registry — exposed by `worker` on `/metrics` (port 8788)
- **API** registry — exposed by `api` middleware
- **Collector** registry — exposed by the standalone `metrics` command, which scrapes cache/Redis/DB state

### Worker metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_fs_operation_duration_seconds` | Histogram | `operation`, `file_type` | Filesystem operation duration; `file_type` is the resource's `core.FileType`, passed by the `resource.Read`/`Write`/`Exists`/`Delete`/`Stat` helpers or `store.WithFileType`, and `Unknown` (first one per operation logged as a warning) when a caller passes none |
| `puckdb_fs_bytes` | Histogram | `operation`, `file_type` | Read/write sizes |
| `puckdb_http_request_duration_seconds` | Histogram | `api`, `method`, `status_code` | External API request duration |
| `puckdb_http_response_bytes` | Histogram | `api` | Response body sizes |
| `puckdb_download_total` | Counter | `file_type`, `result` | Downloads by result (hit/miss/error) |
| `puckdb_activity_duration_seconds` | Histogram | `activity` | Temporal activity duration |

### API metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_http_request_duration_seconds` | Histogram | `api`, `method`, `status_code` | API server request duration (separate registry from the worker's HTTP client metric of the same name) |

### Collector metrics (cache / Redis / DB)
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_cache_files_expected` | Gauge | `season`, `file_type` | Expected file count per season/type |
| `puckdb_cache_files_found` | Gauge | `season`, `file_type` | Actual file count per season/type |
| `puckdb_cache_completeness_percent` | Gauge | `season`, `file_type` | found/expected × 100 |
| `puckdb_cache_stats_last_updated_timestamp` | Gauge | — | Unix ts of last cache scan |
| `puckdb_cache_stats_compute_duration_seconds` | Gauge | — | Time taken by last cache scan |
| `puckdb_cache_disk_size_bytes` | Gauge | — | Bytes used by cache directory |
| `puckdb_data_path_files_total` | Gauge | `file_type` | Total files in data path by type |
| `puckdb_data_path_bytes_total` | Gauge | `file_type` | Total bytes in data path by type |
| `puckdb_redis_oauth_token_valid` | Gauge | `user` | 1 = usable token (fresh access token or a refresh token), 0 = missing/unrefreshable |
| `puckdb_redis_last_updated_timestamp` | Gauge | — | Unix ts of last Redis scan |
| `puckdb_db_table_row_count` | Gauge | `table` | Row count per table |
| `puckdb_db_table_size_bytes` | Gauge | `table` | On-disk size per table (heap + indexes + TOAST) |
| `puckdb_db_size_bytes` | Gauge | — | Total database size |
| `puckdb_db_last_updated_timestamp` | Gauge | — | Unix ts of last DB scan |
| `puckdb_build_info` | Gauge | `version` | Build version of the running binary |

## Database Schema

PostgreSQL database storing NHL game data and Yahoo Fantasy league data. Two main data domains that link via player matching.

### Data Volume (puckdb_prod, snapshot 2026-04-07)
| Table | Rows | Growth |
|-------|------|--------|
| `shifts` | ~14.5M | Per shift per game |
| `play_events` | ~7.8M | Per play (PBP) per game |
| `game_skater_stats` | ~2.26M | Per game per player |
| `yahoo_team_rosters` | ~1.0M | Per day per roster slot |
| `yahoo_team_summary_stats` | ~624K | Per stat per team-day |
| `goal_highlights` | ~400K | Per goal |
| `player_season_totals` | ~319K | Per player per season |
| `standings_snapshots` | ~305K | Per team per day |
| `game_goalie_stats` | ~245K | Per game per goalie |
| `club_skater_stats` | ~76K | Per team per season |
| `games` | ~65K | ~1,300/season |
| `season_rosters` | ~59K | Per season roster slot |
| `yahoo_team_summaries` | ~54K | Per league per team-week |
| `players` | ~9.6K | Slow (new players only) |

### NHL Data

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `players` | NHL players (skaters + goalies) | `id` (NHL ID), `yahoo_id`, `first_name`, `last_name`, `team_id`, `position` |
| `seasons` | NHL seasons | `id` (e.g., 20242025), `standings_start`, `standings_end` |
| `franchises` | NHL franchises (historical) | `id`, `full_name`, `team_common_name` |
| `season_teams` | Teams per season (handles relocations) | `season_id`, `team_id`, `franchise_id`, `abbrev`, `division_name` |
| `season_rosters` | Per-season roster snapshots (PK `(season, team_id, player_id)`) | `season`, `team_id`, `player_id`, `position`, `shoots_catches`, `sweater_number`, `height_inches`, `weight_pounds`, `birth_date`, `birth_country` |
| `games` | Individual games | `id`, `season`, `game_type`, `game_date`, `home_team_id`, `away_team_id`, `game_state` |
| `game_broadcasts` | Broadcast feeds per game (PK `(game_id, broadcast_id)`) | `game_id`, `broadcast_id`, `market`, `country_code`, `network`, `sequence_number` |
| `game_coaches` | Coaches per game (PK `(game_id, team_id)`) | `game_id`, `team_id`, `head_coach` |
| `game_officials` | Referees / linesmen (PK `(game_id, role, sequence)`, role ∈ `{referee, linesman}`) | `game_id`, `role`, `sequence`, `name` |
| `game_scratches` | Scratched players (PK `(game_id, player_id)`) | `game_id`, `team_id`, `player_id` |
| `game_three_stars` | 3 Stars of the game (PK `(game_id, star)`, star 1–3) | `game_id`, `star`, `player_id` |
| `game_skater_stats` | Per-game skater stats | `game_id`, `player_id`, `goals`, `assists`, `points`, `toi_seconds`, `shots_on_goal` |
| `game_goalie_stats` | Per-game goalie stats | `game_id`, `player_id`, `saves`, `goals_against`, `save_pctg`, `decision` |
| `play_events` | Play-by-play events (~7.8M rows). PK `(game_id, event_id)`. ~43 columns (one per event variant) | `game_id`, `event_id`, `period`, `type_desc_key`, `x_coord`, `y_coord`, `shooting_player_id`, `goalie_in_net_id`, `assist1_player_id`, `assist2_player_id`, `committed_by_player_id`, `hitting_player_id`, ... |
| `shifts` | Per-shift TOI data (largest table — ~14.5M rows) | `id` (PK), `game_id`, `player_id`, `team_id`, `period`, `start_time`, `end_time`, `duration`, `shift_number`, `event_number`, `event_description` |
| `even_strength_pair_toi` | Per-game shared even-strength TOI between a skater and an on-ice teammate on the same club, both directions stored; derived from `shifts` + box scores and rebuilt per game in one transaction by the shift chart import, read by the projection linemate query (PK `(game_id, player_id, teammate_id)`, ON DELETE CASCADE from `games`) | `game_id`, `player_id`, `teammate_id`, `shared_toi_seconds` |
| `even_strength_skater_games` | Per-game even-strength TOI and points for one skater; same derivation and rebuild as `even_strength_pair_toi` (PK `(game_id, player_id)`, ON DELETE CASCADE from `games`) | `game_id`, `player_id`, `team_id`, `toi_seconds`, `points` |
| `shootout_attempts` | Shootout attempts (PK `(game_id, sequence)`) | `game_id`, `sequence`, `player_id`, `team_id`, `shot_type`, `result`, `game_winner` |
| `goal_highlights` | Highlight clips per goal (PK `(game_id, event_id)`) | `game_id`, `event_id`, `player_id`, `period`, `time_in_period`, `goals_to_date`, `highlight_clip_id`, `highlight_clip_url`, `discrete_clip_id` |
| `standings_snapshots` | Daily standings per team (PK `(season, date, team_abbrev)` — note `team_abbrev`, not `team_id`) | `season`, `date`, `team_abbrev`, `wins`, `losses`, `ot_losses`, `points`, `division_abbrev`, `division_name`, `conference_abbrev` |
| `club_skater_stats` | Aggregated club skater stats per season (PK `(season, game_type, team_id, player_id)`). Per-club rows: a traded player appears under each club with only that stint's stats; only `regular_season` and `playoffs` are imported | `season`, `game_type`, `team_id`, `player_id`, `games_played`, `goals`, `assists`, `points`, `shots`, `shooting_pctg`, `avg_toi_per_game`, `faceoff_win_pctg` |
| `club_goalie_stats` | Aggregated club goalie stats per season (PK `(season, game_type, team_id, player_id)`). Same per-club and game-type semantics as `club_skater_stats` | `season`, `game_type`, `team_id`, `player_id`, `games_played`, `wins`, `losses`, `overtime_losses`, `goals_against_average`, `save_percentage`, `shutouts`, `toi_seconds` |
| `player_season_totals` | Per-season aggregates per player, **including minor leagues** (PK `(player_id, season, game_type, league_abbrev, sequence)`) | `player_id`, `season`, `game_type`, `league_abbrev`, `sequence`, `team_name`, `games_played`, `goals`, `assists`, `points`, `plus_minus`, `pim` |
| `player_awards` | NHL awards / trophies (PK `(player_id, trophy_name, season)`) | `player_id`, `trophy_name`, `season` |

### NHL Edge Tracking Data (2021-2022 onward)

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `edge_skater_stats` | Per-skater Edge summary (PK `(player_id, season, game_type)`) | `top_speed_imperial/metric`, `bursts_over_20`, `total_distance_imperial/metric`, `top_shot_speed_imperial/metric`, `oz/nz/dz_pctg` + percentiles and league avgs |
| `edge_skater_shot_locations` | Shot locations by rink area (PK `(player_id, season, game_type, area)`) | `sog`, `goals`, `shooting_pctg` + percentiles |
| `edge_skater_sog_summary` | SOG summary by location code (PK `(player_id, season, game_type, location_code)`) | `shots`, `goals`, `shooting_pctg` + percentiles and league avgs |
| `edge_goalie_stats` | Per-goalie Edge summary (PK `(player_id, season, game_type)`) | `gaa_value`, `games_above_900_value`, `goal_diff_per_60_value`, `point_pctg_value` + percentiles and league avgs |
| `edge_goalie_shot_location_summary` | Goalie saves by location code (PK `(player_id, season, game_type, location_code)`) | `goals_against`, `saves`, `save_pctg` + percentiles and league avgs |
| `edge_goalie_shot_locations` | Goalie saves by rink area (PK `(player_id, season, game_type, area)`) | `saves`, `save_pctg` + percentiles |
| `edge_team_stats` | Per-team Edge summary (PK `(team_id, season, game_type)`) | `shot_attempts_over_90`, `top_shot_speed_imperial/metric`, `speed_max_imperial/metric`, `bursts_over_22/20`, `total_distance`, `oz/nz/dz_pctg` + ranks and league avgs |
| `edge_team_sog_summary` | Team SOG by location code (PK `(team_id, season, game_type, location_code)`) | `shots`, `goals`, `shooting_pctg` + ranks and league avgs |
| `edge_team_shot_locations` | Team shot locations by area (PK `(team_id, season, game_type, area)`) | `shots`, `shots_rank` |
| `edge_team_zone_time_by_strength` | Zone time by strength code (PK `(team_id, season, game_type, strength_code)`) | `oz/nz/dz_pctg` + ranks |
| `edge_team_shot_differential` | Shot differential by strength (PK `(team_id, season, game_type, strength_code)`) | `for_per_game`, `against_per_game`, `differential_per_game` + ranks |

**Game types:** 1=preseason, 2=regular, 3=playoffs
**Game states:** FUT=future, LIVE=in progress, OFF/FINAL=completed

### Yahoo Fantasy Data

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `yahoo_leagues` | Fantasy leagues | `id`, `league_key`, `name`, `season`, `num_teams`, `scoring_type` |
| `yahoo_teams` | Fantasy teams in leagues | `league_id`, `id`, `team_key`, `name`, `is_owned_by_current_login` |
| `yahoo_team_rosters` | Daily roster snapshots | `league_id`, `team_id`, `date`, `player_id`, `selected_position` |
| `yahoo_team_managers` | Team managers | `league_id`, `team_id`, `manager_id`, `nickname` |
| `yahoo_team_summaries` | Team standings/records (per snapshot) | `league_id`, `team_id`, `rank`, `wins`, `losses` |
| `yahoo_team_summary_stats` | Per-stat values for team summaries (PK `(league_id, team_id, date, stat_id)`, FK → `yahoo_team_summaries`) | `league_id`, `team_id`, `date`, `stat_id`, `value` |
| `yahoo_matchups` | Head-to-head matchups (PK `(league_id, week, team1_id, team2_id)`) | `league_id`, `week`, `team1_id`, `team2_id`, `team1_points`, `team2_points`, `status`, `is_playoffs`, `is_consolation` |
| `yahoo_draft_results` | Draft picks (PK `(league_id, round, pick)`) | `league_id`, `round`, `pick`, `team_id`, `player_id`, `cost` |
| `yahoo_transactions` | Adds, drops, trades (PK `(league_id, transaction_key)`). `players` is a JSONB blob | `league_id`, `transaction_key`, `type`, `timestamp`, `status`, `players` (jsonb) |
| `yahoo_league_stat_categories` | Scoring categories | `league_id`, `stat_id`, `name`, `sort_order` (1 higher / 0 lower is better, NULL unknown), `position_type`, `is_only_display_stat`, `value` (points weight) |
| `yahoo_league_roster_positions` | Roster position config | `league_id`, `position`, `count` |
| `yahoo_league_rule_snapshots` | Versioned normalized league rules (UNIQUE `(season, league_key, rules_hash)`); latest = max `last_seen_at` | `season`, `league_id`, `league_key`, `game_key`, `source` (`yahoo_api`/`temporary_stand_in`), `source_league_key`, `fetched_at`, `rules` (jsonb) |
| `yahoo_league_players` | Draftable pool with league eligibility/status (PK `(league_key, player_id)`) | `season`, `league_id`, `player_key`, `eligible_positions`, `status`, `injury_note`, `fetched_at` |

**Yahoo league identity:** Yahoo league IDs are unique only within one season (game key). The legacy `yahoo_*` tables key by numeric `league_id` alone; the importer refuses to overwrite a `yahoo_leagues` row that belongs to another season. The draft helper tables key by `league_key` + season.

### Player news tables (draft helper)

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `news_fetch_state` | Fetch coverage per source and scope (PK `(source_id, scope)`); a failure never clears the last success | `etag`, `last_modified`, `body_hash`, `last_attempt_at`, `last_success_at`, `data_as_of`, `last_error`, `consecutive_failures` |
| `news_articles` | One row per story (UNIQUE `(publisher, external_id)`) | `source_id`, `kind` (`official`/`structured`/`reporting`), `url`, `first_seen_at`, `last_seen_at`, `source_updated_at` |
| `news_article_versions` | Each distinct content of a story (UNIQUE `(article_id, version)`); unprocessed while `processed_at` is NULL. `evidence_text` is the bounded summary; `body` the full text when the source offers it | `content_hash`, `title_fingerprint`, `text_fingerprint`, `title`, `evidence_text`, `body`, `published_at`, `source_updated_at`, `retrieved_at`, `subjects` (jsonb), `team_hints`, `category_hint` |
| `news_mentions` | Player references per version (PK `(version_id, ordinal)`) | `role` (`subject`/`mentioned`), `resolution` (`resolved`/`ambiguous`/`unresolved`), `method`, `nhl_player_id`, `yahoo_player_id`, `candidates` (jsonb) |
| `news_incidents` | Incident candidates: one per player, category and time window, however many reports repeat it | `nhl_player_id`, `yahoo_player_id`, `category`, `first_reported_at`, `last_reported_at` |
| `news_incident_evidence` | Versions behind an incident (PK `(incident_id, version_id)`) | `publisher`, `kind`, `reported_at`, `relation` (`independent`/`syndicated`/`same_publisher`/`revision`) |
| `news_extractions` | LLM extraction per version and extractor (UNIQUE `(version_id, extractor_key)`; key = provider/model/prompt/schema version) | `input_hash` (cache), `status` (`pending`/`succeeded`/`invalid`/`failed`), `attempts`, `last_error`, `raw_output`, `issues` (jsonb), tokens, `cached_from_id`, `reconciled_at` |
| `news_events` | Validated events; facts immutable, new details = new event | `nhl_player_id`, `yahoo_player_id`, `event_type`, `report_status` (`confirmed`/`reported`/`rumor`), `effective_from`, `duration_kind`/`_games`/`_days`/`_until`, `change_field`/`_from`/`_to`, `lifecycle` (`active`/`superseded`/`retracted`/`resolved`), `superseded_by`, `incident_id`, `needs_review`, `review_reason` |
| `news_event_evidence` | Versions behind an event per extraction (PK `(event_id, version_id, extraction_id, relation)`) | `relation` (`supports`/`contradicts`/`retracts`/`resolves`/`withdraws`), `quotes` (jsonb), `publisher`, `kind`, `reported_at` |
| `news_event_transitions` | Audit trail of every event creation and lifecycle change | `event_id`, `from_lifecycle`, `to_lifecycle`, `version_id`, `extraction_id`, `reason`, `at` |
| `news_extraction_evaluations` | Runs of the labeled corpus per extractor; the latest passing run on the current corpus version gates automatic effects | `extractor_key`, `corpus_version`, `passed`, `metrics` (jsonb) |

### News adjustment tables (draft helper)

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `news_adjustment_overrides` | Manager overrides; never deleted, ended by `reset_at` or `expires_at` | `id`, `player_key`, `league_key`, `kind` (`missed_games`/`input`/`exclude_event`), `event_id`, `scenario`, `input`, `value`, `reason`, `created_at` |
| `news_adjustment_runs` | One adjustment of a baseline projection snapshot (UNIQUE `adjustment_id`) | `policy` (jsonb), `baseline_snapshot_id`, `league_key`, `as_of`, `season`, `overrides` (as of `as_of`), `coverage_warnings` |
| `news_adjustment_events` | Event versions a run saw (PK `(run_id, event_id)`) | `version`, `incident_id`, `outcome`, `reason`, `scenarios`, `event` (jsonb) |
| `news_adjustment_scenarios` | Adjusted projection snapshot per scenario (PK `(run_id, scenario)`) | `snapshot_id` → `projection_snapshots` |
| `news_adjustment_players` | Per-player explanation (PK `(run_id, player_key)`) | `adjustment` (jsonb) |

### Draft ranking tables

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `draft_ranking_snapshots` | One successful ranking refresh of a league; immutable, latest by `as_of` is served | `season`, `league_id`, `league_key`, `identity`, `rules_hash`, `projection_snapshot_id`, `adjustment_run_id`, `as_of`, `meta` (jsonb) |
| `draft_ranking_players` | Every pool player of a snapshot with each scenario placement (PK `(snapshot_id, player_key)`) | `baseline_rank`, `player` (jsonb) |
| `draft_ranking_refreshes` | Refresh attempts per league (UNIQUE `(run_id, season, league_id)`) | `status` (`running`/`succeeded`/`failed`/`canceled`), `state` (issue code), `error`, `snapshot_id`, `started_at`, `finished_at` |
| `draft_sessions` | Full-league-key live board and manual overlay | `state_version`, `sync_version`, `board`, `draft_status`, freshness/error timestamps, recommendation safety |
| `draft_session_observations` | Every Yahoo watch attempt for capability measurement | duration, authority, counts, change, error class |
| `draft_session_events` | Versioned board/manual changes; identical polls add no event | `state_version`, `kind`, `details`, `created_at` |
| `draft_shortlist` | Persisted Maurice shortlist per full league key | `league_key`, `player_key`, `created_at` |
| `draft_recommendation_runs` | Replayable deterministic draft advice input/output | session/ranking/projection/rule versions, strategy, numeric reasons, latency |

### Maurice (LLM chat) tables

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `app_users` | Local projection of an Authentik identity (UNIQUE `(auth_provider, auth_subject)`; username/display name are mutable snapshots) | `id` (uuid), `auth_provider`, `auth_subject` (`X-authentik-uid`), `username`, `display_name`, `first_seen_at`, `profile_updated_at`, `disabled_at` |
| `app_sessions` | One PuckDB application session; only a keyed hash of the cookie is stored | `user_id`, `session_key_hash` (UNIQUE), `started_at`, `last_activity_at`, `ended_at` (idle expiry) |
| `maurice_conversations` | One row per chat session, owned by a user (every query is scoped by `user_id`). Deleting sets `deleted_at` (hidden from the owner, rows and usage kept) | `id` (uuid), `user_id`, `title`, `created_at`, `updated_at`, `deleted_at` |
| `maurice_turns` | One prompt and all the work behind its answer. UNIQUE `(user_id, idempotency_key)`, one `running` turn per conversation (partial unique index); a running turn older than the stale cutoff is failed as `abandoned` | `conversation_id`, `turn_number`, `idempotency_key`, `request_hash`, `status` (`running`/`succeeded`/`failed`/`cancelled`), `started_at`, `completed_at`, `error_class` (bounded label) |
| `maurice_messages` | Messages of a turn (UNIQUE `(turn_id, message_number)`). `role` ∈ `{system, user, assistant, tool}`, `tool_calls` JSONB. The transcript (history replayed to the LLM) is only the messages of `succeeded` turns; failed/cancelled turns keep theirs as exact prompts | `id` (uuid), `conversation_id`, `turn_id`, `message_number`, `role`, `content`, `tool_calls`, `tool_call_id`, `created_at` |
| `maurice_llm_calls` | One provider request: `chat_round`, `forced_final`, or `title_generation` (no turn). Token counts are provider-reported and NULL when not reported | `turn_id`, `conversation_id`, `call_kind`, `round_number`, `provider`, `model`, `provider_request_id`, `status`, `finish_reason`, `started_at`, `completed_at`, `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `error_class`, `system_prompt`, `instruction`, `tool_definitions` |
| `maurice_llm_call_messages` | Ordered message references of each call's request (system prompt excluded), so every prompt can be rebuilt without copying content (PK `(llm_call_id, input_number)`) | `llm_call_id`, `input_number`, `message_id` |
| `maurice_tool_calls` | Each tool call a response requested (UNIQUE `(llm_call_id, sequence_number)`) | `turn_id`, `llm_call_id`, `tool_name`, `arguments` (jsonb, NULL if invalid), `arguments_raw`, `result`, `status` (`succeeded`/`tool_error`/`parse_error`/`cancelled` = never ran), `started_at`, `completed_at` |

A turn's messages, calls, call-message links and tool calls commit in one transaction after the tool loop (`FinishTurn`); no transaction stays open across model calls. Prompts and tool payloads are kept forever (deleting a conversation only hides it); legacy conversations without an owner were deleted by migration 000022. There is no analytics API, export or erase capability.

### Views

Reporting views (definitions in migrations): `skater_season_stats`, `skater_recent_stats`, `goalie_season_stats`, `goalie_recent_stats`, `yahoo_roster_players`, `yahoo_roto_standings`, `yahoo_season_team_totals`, `app_user_usage` (per user: `puckdb_session_count`, last session, turns, LLM calls, token sums including title generation, `active_seconds` = turn durations + title calls, i.e. time spent processing prompts).

### Key Relationships

```
players.id ←──── game_skater_stats.player_id
players.id ←──── game_goalie_stats.player_id
players.yahoo_id ───→ yahoo_team_rosters.player_id (matched via enrichment)

seasons.id ←──── season_teams.season_id
seasons.id ←──── games.season

franchises.id ←──── season_teams.franchise_id

games.id ←──── game_skater_stats.game_id
games.id ←──── game_goalie_stats.game_id

yahoo_leagues.id ←──── yahoo_teams.league_id
yahoo_teams.(league_id, id) ←──── yahoo_team_rosters.(league_id, team_id)
```

### Common Queries

```sql
-- Player season totals
SELECT p.first_name, p.last_name, SUM(s.goals), SUM(s.assists)
FROM players p
JOIN game_skater_stats s ON p.id = s.player_id
JOIN games g ON s.game_id = g.id
WHERE g.season = 20242025 AND g.game_type = 2
GROUP BY p.id;

-- Team roster on a date
SELECT p.first_name, p.last_name, r.selected_position
FROM yahoo_team_rosters r
JOIN players p ON p.yahoo_id = r.player_id
WHERE r.league_id = 12345 AND r.team_id = 1 AND r.date = '2024-12-01';
```

## Maurice (AI Chat)

Interactive REPL for querying hockey data via natural language. Connects to an LLM and uses MCP tools to query PostgreSQL.

### Architecture

| Component | File | Purpose |
|-----------|------|---------|
| CLI entry | `cmd/maurice.go` | REPL loop, `/model` `/history` `/load` `/new` commands |
| Service | `internal/maurice/service.go` | Chat orchestration, tool call loop (max 10 rounds), conversation persistence |
| Prompt | `internal/maurice/prompt.go` | System prompt (instructs LLM to query DB, not guess) |
| LLM clients | `internal/llm/client.go`, `internal/llm/anthropic.go` | OpenAI-compatible (Ollama/OpenAI) and Anthropic clients |
| Provider detection | `internal/llm/provider.go` | Auto-detects Anthropic vs OpenAI-compatible from API key/URL |
| MCP integration | `internal/mcp/` | Connects to puckdb MCP server for database tool calls |
| Persistence | `~/.puckdb/maurice.db` | SQLite for the REPL (single user `maurice.LocalUserID`; turns and transcript, no LLM-call usage); the API uses PostgreSQL (`maurice.NewPgDB`) |

### Configuration

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `maurice-model` | `PUCKDB_MAURICE_MODEL` | `claude-sonnet-5-5` | Model ID; must be in `mauriceModels` (`cmd/maurice.go`), which also picks the provider. Unknown IDs fail startup |
| `ollama-base-url` | `PUCKDB_OLLAMA_BASE_URL` | `http://localhost:11434/v1` | Ollama (OpenAI-compatible) endpoint |
| `anthropic-api-key` | `PUCKDB_ANTHROPIC_API_KEY` | (empty) | Required for Anthropic models |
| `openai-api-key` | `PUCKDB_OPENAI_API_KEY` | (empty) | Required for OpenAI models |
| `maurice-config` | `PUCKDB_MAURICE_CONFIG` | `~/.puckdb/maurice.yaml` | Path to Maurice config file |
| `maurice-max-tokens` | `PUCKDB_MAURICE_MAX_TOKENS` | `4096` | Max response tokens |
| `maurice-max-history` | `PUCKDB_MAURICE_MAX_HISTORY` | `50` | Conversation history depth |
| `maurice-max-tool-rounds` | `PUCKDB_MAURICE_MAX_TOOL_ROUNDS` | `10` | Max tool-call rounds per message |

### REPL Commands

| Command | Action |
|---------|--------|
| `/model` | Show current model + suggestions |
| `/model <name>` | Switch model (rebuilds LLM client) |
| `/new` | Start new conversation |
| `/history` | List recent conversations |
| `/load <id>` | Resume a conversation |
| `/quit` | Exit |

### Prompt format

```
maurice (<model-name>) >
```

## Testing Guidelines

Always run the full test suite (`go test ./...`) and verify 100% pass rate before committing any changes. Do not commit if any tests fail.

### Database-backed tests

Tests that need a real PostgreSQL read `PUCKDB_TEST_PG_URL` and skip when it is unset. Point it at a dedicated throwaway database — the harnesses migrate and truncate it:

```bash
docker run -d --rm --name puckdb-test-pg -e POSTGRES_USER=puckdb -e POSTGRES_PASSWORD=foo \
  -e POSTGRES_DB=puckdb_test -p 15433:5432 postgres:16-alpine
PUCKDB_TEST_PG_URL='postgres://puckdb:foo@localhost:15433/puckdb_test?sslmode=disable' go test ./internal/maurice/
```

`internal/maurice/dbcontract_test.go` is the shared persistence contract for the Maurice `DB` implementations; SQLite runs it in-memory on every `go test`, PostgreSQL runs it only with the env var set. Any behaviour change to one adapter must keep both passing.

### Serialization Error Handling

When testing code that serializes/deserializes domain objects:

1. **Never ignore marshal/unmarshal errors** - Use `require.NoError(t, err)` instead of `_, _ :=`
2. **Use realistic test data** - Types like `nhl.Position` and `nhl.Season` have custom marshalers that require valid values
3. **Understand mock vs storage patterns**:
   - **Mock returns**: Pass structs directly, no serialization validation
   - **Storage pre-population**: Goes through JSON round-trip, all fields must be valid

## References

- [Yahoo Fantasy Sports API Guide](https://developer.yahoo.com/fantasysports/guide/)
- [OAuth2 Example](https://stackoverflow.com/questions/48255130/yahoo-fantasy-sports-example-using-oauth2)
