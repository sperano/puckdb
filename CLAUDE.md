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
go run github.com/99designs/gqlgen generate   # GraphQL (from puckdb root dir)
```

**GraphQL code generation:**
1. Edit `graph/schema.graphqls` (schema, types, queries, mutations)
2. Run `go run github.com/99designs/gqlgen generate` from puckdb directory
3. Generated files: `graph/generated/generated.go`, `graph/model/models_gen.go`
4. New resolvers appear as `panic("not implemented")` stubs in `graph/schema.resolvers.go`
5. Implement resolver logic in `graph/resolver.go` (private methods like `fetchPlayerLandings`)
6. Update `schema.resolvers.go` stubs to call the new `resolver.go` methods

## CLI Commands

| Command | Description |
|---------|-------------|
| `api` | HTTP server: GraphQL at `/graphql`, playground at `/graphql/`, OAuth at `/yahoo/*` |
| `worker` | Temporal worker for download/import workflows, metrics at `/metrics` |
| `sync` | Sync data into the database |
| `metrics` | Expose cache, Redis, and database metrics as Prometheus metrics |
| `db init` | Create tables, seed NHL data |
| `db drop` | Drop all tables |
| `db migrate` | Run database migrations directly |
| `db force-version <version>` | Clear a dirty migration state after inspecting the schema (see `docs/migration-recovery.md`) |
| `db check-teams` | Report regular-season/playoff games whose team has no `season_teams` row (such games vanish from games queries) |
| `db provision` | Create database/user on shared PostgreSQL |
| `redis flush` | Flush a Redis database |
| `yahoo signout` | Clear OAuth2 token from Redis |
| `maurice` | Interactive AI hockey chat REPL |

## Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/` | CLI commands (Cobra + Viper) |
| `worker/` | Temporal workflows and activities, split into `workflow/` (top-level workflows), `nhl/`, `yahoo/`, `player/`, `admin/`, `shared/` |
| `graph/` | GraphQL resolvers and schema (gqlgen) |
| `database/` | PostgreSQL connection (pgx), migrations |
| `sqlcdb/` | sqlc-generated type-safe queries |
| `cache/` | File-based caching, XML/JSON parsing |
| `store/` | Storage backends (filesystem, in-memory, instrumented) for raw cached files |
| `resource/` | Typed resource definitions (NHL/Yahoo paths, URLs, parse/format) |
| `core/` | Shared primitives: file types, data origins, time helpers, resource interfaces |
| `config/` | Flags, defaults, seasons YAML parsing |
| `http/` | HTTP client, Yahoo API URL builders |
| `metrics/` | Prometheus metrics |
| `temporal/` | Temporal client configuration |
| `matching/` | NHL ↔ Yahoo player matching |
| `llm/` | LLM client (used by player enrichment / Maurice) |
| `maurice/` | Prompt + service layer built on top of `llm/` |
| `mcp/` | MCP client / tool integration |
| `tls/` | TLS certificates for internal services |

### Active Workflows

Defined in `worker/workflow/`:
- `FetchSeasonsWorkflow` / `FetchSeasonWorkflow` — NHL season data (parent + child)
- `ImportSeasonsWorkflow` / `ImportSeasonWorkflow` — Parse cached files into Postgres
- `FetchPlayerLogsWorkflow` / `FetchSeasonPlayerLogsWorkflow` — Per-player game logs
- `ImportPlayerLogsWorkflow` / `ImportSeasonPlayerLogsWorkflow` — Import those logs
- `FetchPlayerLandingsWorkflow` — NHL player landing pages
- `ExtractBoxscorePlayersWorkflow` — Extract player rows from boxscores
- `FetchYahooPlayersWorkflow` — Yahoo player pages
- `ProcessPlayersWorkflow` — Player enrichment and matching
- `FetchEdgeSeasonsWorkflow` / `FetchEdgeWorkflow` — NHL Edge tracking data (2021-2022+)
- `ImportEdgeSeasonsWorkflow` / `ImportEdgeWorkflow` — Import cached Edge data into Postgres
- `InitializeWorkflow` — Database initialization

Defined in `worker/admin/`:
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
- `FetchSeasonWorkflow` — uses `RunWorkerPool` for automatic tracking
- `FetchEdgeWorkflow` — uses manual `IncrementBar` calls

### Configuration Inside Workflow Code

Workflow code must never read viper flags or config files directly. Any setting that shapes the command sequence (concurrency, batch sizes, which Yahoo leagues to process) is resolved **once at the start of the workflow** through `shared.SnapshotConfig` / `shared.SnapshotConfigInt`, which records the value in history via `SideEffect` so a replay after a worker restart reuses it instead of re-reading changed local settings. ContinueAsNew runs carry the snapshot in their input (see `FetchYahooPlayersInput.Config`). Activity options (timeouts, retry policy) are not part of the determinism check and may stay live. Reference: `worker/workflow/season_config.go`.

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

Flags follow a strict pattern in `config/`:

1. **Flag names** → constants in `flags.go` (e.g., `FlagAPIPort = "api-port"`)
2. **Defaults** → constants in `defaults.go` (e.g., `DefaultAPIPort = 8787`)
3. **No magic numbers** in flag definitions
4. Each flag has `Init*Flag()` and `Bind*Flag()` functions
5. Cmd files only call these functions, never define flags locally

Season config: `seasons.yaml` (start/end dates, game keys, league IDs, team IDs)

## GraphQL API

Schema lives in `graph/schema.graphqls`. Each long-running workflow follows the same pattern: a `start` mutation, a matching `cancel*` mutation, a `*Result` query, and a `*Progress` query.

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

**Admin mutations:** `clearDatabase`, `dropDatabase`, `createDatabase`, `flushRedisDB`

**Maurice (LLM chat) mutations:** `mauriceChat(conversationId, message)`, `mauriceDeleteConversation(id)`

**Workflow queries:** for every workflow above, `<name>Result: WorkflowResult!` and `<name>Progress: ProgressReport`. `processPlayers` additionally exposes `processPlayersResultData: ProcessPlayersResultData`.

**Data queries** (in `graph/data.graphqls`): `seasons`, `teams`, `players`, `games`, `standings`, `boxscore`, `skaterGameLog`, `goalieGameLog`, `playerSeasonTotals`, `edgeSkaterStats`, `edgeGoalieStats`, `edgeTeamStats`

**Other queries:** `buildNumber`, `yahooTokenStatus`, `mauriceConversations(limit)`, `mauriceConversation(id)`

## Metrics

The Prometheus registry is split in three (see `metrics/metrics.go`):
- **Worker** registry — exposed by `worker` on `/metrics` (port 8788)
- **API** registry — exposed by `api` middleware
- **Collector** registry — exposed by the standalone `metrics` command, which scrapes cache/Redis/DB state

### Worker metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_fs_operation_duration_seconds` | Histogram | `operation`, `file_type` | Filesystem operation duration |
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
| `shootout_attempts` | Shootout attempts (PK `(game_id, sequence)`) | `game_id`, `sequence`, `player_id`, `team_id`, `shot_type`, `result`, `game_winner` |
| `goal_highlights` | Highlight clips per goal (PK `(game_id, event_id)`) | `game_id`, `event_id`, `player_id`, `period`, `time_in_period`, `goals_to_date`, `highlight_clip_id`, `highlight_clip_url`, `discrete_clip_id` |
| `standings_snapshots` | Daily standings per team (PK `(season, date, team_abbrev)` — note `team_abbrev`, not `team_id`) | `season`, `date`, `team_abbrev`, `wins`, `losses`, `ot_losses`, `points`, `division_abbrev`, `division_name`, `conference_abbrev` |
| `club_skater_stats` | Aggregated club skater stats per season (PK `(season, game_type, team_id, player_id)`) | `season`, `game_type`, `team_id`, `player_id`, `games_played`, `goals`, `assists`, `points`, `shots`, `shooting_pctg`, `avg_toi_per_game`, `faceoff_win_pctg` |
| `club_goalie_stats` | Aggregated club goalie stats per season (PK `(season, game_type, team_id, player_id)`) | `season`, `game_type`, `team_id`, `player_id`, `games_played`, `wins`, `losses`, `overtime_losses`, `goals_against_average`, `save_percentage`, `shutouts`, `toi_seconds` |
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
| `yahoo_league_stat_categories` | Scoring categories | `league_id`, `stat_id`, `name`, `is_only_display_stat` |
| `yahoo_league_roster_positions` | Roster position config | `league_id`, `position`, `count` |

### Maurice (LLM chat) tables

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `maurice_conversations` | One row per chat session (UUID PK, default `gen_random_uuid()`) | `id` (uuid), `title`, `created_at`, `updated_at` |
| `maurice_messages` | Individual messages within a conversation. `role` ∈ `{system, user, assistant, tool}`. `tool_calls` stored as JSONB. ON DELETE CASCADE from conversations | `id` (uuid), `conversation_id`, `role`, `content`, `tool_calls` (jsonb), `tool_call_id`, `created_at` |

### Views

Reporting views (definitions in migrations): `skater_season_stats`, `skater_recent_stats`, `goalie_season_stats`, `goalie_recent_stats`, `yahoo_roster_players`, `yahoo_roto_standings`, `yahoo_season_team_totals`.

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
| Service | `maurice/service.go` | Chat orchestration, tool call loop (max 10 rounds), conversation persistence |
| Prompt | `maurice/prompt.go` | System prompt (instructs LLM to query DB, not guess) |
| LLM clients | `llm/client.go`, `llm/anthropic.go` | OpenAI-compatible (Ollama/OpenAI) and Anthropic clients |
| Provider detection | `llm/provider.go` | Auto-detects Anthropic vs OpenAI-compatible from API key/URL |
| MCP integration | `mcp/` | Connects to puckdb MCP server for database tool calls |
| Persistence | `~/.puckdb/maurice.db` | SQLite for conversation history |

### Configuration

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `maurice-model` | `PUCKDB_MAURICE_MODEL` | `llama3.1:8b` | LLM model name |
| `maurice-base-url` | `PUCKDB_MAURICE_BASE_URL` | `http://localhost:11434/v1` | LLM endpoint (Ollama default) |
| `maurice-api-key` | `PUCKDB_MAURICE_API_KEY` | (empty) | API key (required for Anthropic/OpenAI) |
| `maurice-config` | `PUCKDB_MAURICE_CONFIG` | `~/.puckdb/maurice.yaml` | Path to Maurice config file |
| `maurice-max-tokens` | `PUCKDB_MAURICE_MAX_TOKENS` | `4096` | Max response tokens |
| `maurice-max-history` | `PUCKDB_MAURICE_MAX_HISTORY` | `50` | Conversation history depth |

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
PUCKDB_TEST_PG_URL='postgres://puckdb:foo@localhost:15433/puckdb_test?sslmode=disable' go test ./maurice/
```

`maurice/dbcontract_test.go` is the shared persistence contract for the Maurice `DB` implementations; SQLite runs it in-memory on every `go test`, PostgreSQL runs it only with the env var set. Any behaviour change to one adapter must keep both passing.

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
