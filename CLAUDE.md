# PuckDB

Go application that imports NHL hockey data and Yahoo Fantasy Sports data. Downloads from NHL API and Yahoo API, caches locally as files, stores in PostgreSQL. Temporal orchestrates long-running workflows.

## Quick Reference

### Build
```bash
cd /Users/eric/code/workspaces/puckdb/puckdb && go build -o /tmp/puckdb .
```
> **Note:** Always `cd` into the module directory before `go build`. The `-C` flag doesn't work reliably outside a module context.

### Run
```bash
/tmp/puckdb api                      # GraphQL server (port 8787)
/tmp/puckdb worker                   # Temporal worker (port 8788)
/tmp/puckdb info                     # Show configuration
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
| `info` | Display current configuration |
| `cache-check` | Verify cache file completeness |
| `db init` | Create tables, seed NHL data |
| `db drop` | Drop all tables |
| `db provision` | Create database/user on shared PostgreSQL |
| `workflow download` | Trigger download workflows, monitor progress |
| `workflow cancel` | Cancel running Temporal workflows |
| `yahoo signout` | Clear OAuth2 token from Redis |

## Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/` | CLI commands (Cobra + Viper) |
| `worker/` | Temporal workflows and activities |
| `graph/` | GraphQL resolvers and schema (gqlgen) |
| `database/` | PostgreSQL connection (pgx), migrations |
| `sqlcdb/` | sqlc-generated type-safe queries |
| `cache/` | File-based caching, XML/JSON parsing |
| `redis/` | OAuth2 tokens, player data cache |
| `config/` | Flags, defaults, seasons YAML parsing |
| `http/` | HTTP client, Yahoo API URL builders |
| `metrics/` | Prometheus metrics |
| `temporal/` | Temporal client configuration |

### Active Workflows
- `FetchSeasonsWorkflow` - NHL season data
- `FetchYahooPlayersWorkflow` - Yahoo player pages
- `FetchSeasonWorkflow` - Single season data (child workflow)
- `ProcessPlayersWorkflow` - Player enrichment and matching
- `InitializeWorkflow` - Database initialization

Task queue: `puckdb-tasks`

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

**Mutations:**
- `fetchSeasons` / `cancelFetchSeasons` - NHL and Yahoo season data
- `fetchYahooPlayers` / `cancelFetchYahooPlayers` - Yahoo player pages
- `processPlayers` / `cancelProcessPlayers` - Player processing
- `initialize` / `cancelInitialize` - Database initialization
- `clearDatabase`, `dropDatabase`, `createDatabase`, `flushRedisDB`

**Queries:**
- `fetchSeasonsResult`, `fetchSeasonsProgress`
- `fetchYahooPlayersResult`, `fetchYahooPlayersProgress`
- `processPlayersResult`, `processPlayersProgress`, `processPlayersResultData`
- `initializeResult`, `initializeProgress`, `initializeResultData`

## Metrics

Worker exposes Prometheus metrics at `/metrics` (port 8788).

| Metric | Type | Description |
|--------|------|-------------|
| `puckdb_fs_operation_duration_seconds` | Histogram | Filesystem operation duration |
| `puckdb_fs_bytes` | Histogram | Read/write sizes |
| `puckdb_http_request_duration_seconds` | Histogram | External API request duration |
| `puckdb_http_response_bytes` | Histogram | Response body sizes |
| `puckdb_download_total` | Counter | Downloads by result (hit/miss/error) |
| `puckdb_activity_duration_seconds` | Histogram | Temporal activity duration |

## Database Schema

PostgreSQL database storing NHL game data and Yahoo Fantasy league data. Two main data domains that link via player matching.

### Data Volume
| Table | Rows | Growth |
|-------|------|--------|
| game_skater_stats | ~2.2M | Per game per player |
| yahoo_team_rosters | ~250K | Per day per roster slot |
| game_goalie_stats | ~244K | Per game per goalie |
| games | ~65K | ~1,300/season |
| players | ~9.4K | Slow (new players only) |

### NHL Data

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `players` | NHL players (skaters + goalies) | `id` (NHL ID), `yahoo_id`, `first_name`, `last_name`, `team_id`, `position` |
| `seasons` | NHL seasons | `id` (e.g., 20242025), `standings_start`, `standings_end` |
| `franchises` | NHL franchises (historical) | `id`, `full_name`, `team_common_name` |
| `season_teams` | Teams per season (handles relocations) | `season_id`, `team_id`, `franchise_id`, `abbrev`, `division_name` |
| `games` | Individual games | `id`, `season`, `game_type`, `game_date`, `home_team_id`, `away_team_id`, `game_state` |
| `game_skater_stats` | Per-game skater stats | `game_id`, `player_id`, `goals`, `assists`, `points`, `toi_seconds`, `shots_on_goal` |
| `game_goalie_stats` | Per-game goalie stats | `game_id`, `player_id`, `saves`, `goals_against`, `save_pctg`, `decision` |

**Game types:** 1=preseason, 2=regular, 3=playoffs
**Game states:** FUT=future, LIVE=in progress, OFF/FINAL=completed

### Yahoo Fantasy Data

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `yahoo_leagues` | Fantasy leagues | `id`, `league_key`, `name`, `season`, `num_teams`, `scoring_type` |
| `yahoo_teams` | Fantasy teams in leagues | `league_id`, `id`, `team_key`, `name`, `is_owned_by_current_login` |
| `yahoo_team_rosters` | Daily roster snapshots | `league_id`, `team_id`, `date`, `player_id`, `selected_position` |
| `yahoo_team_managers` | Team managers | `league_id`, `team_id`, `manager_id`, `nickname` |
| `yahoo_team_summaries` | Team standings/records | `league_id`, `team_id`, `rank`, `wins`, `losses` |
| `yahoo_league_stat_categories` | Scoring categories | `league_id`, `stat_id`, `name`, `is_only_display_stat` |
| `yahoo_league_roster_positions` | Roster position config | `league_id`, `position`, `count` |

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

## References

- [Yahoo Fantasy Sports API Guide](https://developer.yahoo.com/fantasysports/guide/)
- [OAuth2 Example](https://stackoverflow.com/questions/48255130/yahoo-fantasy-sports-example-using-oauth2)
