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
go run github.com/99designs/gqlgen generate   # GraphQL (schema: graph/schema.graphqls)
```

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
| `database/` | GORM models for PostgreSQL |
| `sqlcdb/` | sqlc-generated type-safe queries |
| `cache/` | File-based caching, XML/JSON parsing |
| `redis/` | OAuth2 tokens, player data cache |
| `config/` | Flags, defaults, seasons YAML parsing |
| `http/` | HTTP client, Yahoo API URL builders |
| `metrics/` | Prometheus metrics |
| `temporal/` | Temporal client configuration |

### Active Workflows
- `DownloadSeasonsWorkflow` - NHL season data
- `DownloadYahooPlayersWorkflow` - Yahoo player pages
- `DownloadRosterForTeamWorkflow` - Team rosters
- `DownloadTeamSummariesForTeamWorkflow` - Team summaries
- `EnrichPlayersWorkflow` - Player enrichment

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
- `downloadSeasons` / `cancelDownloadSeasons` - NHL and Yahoo season data
- `downloadYahooPlayers` / `cancelDownloadYahooPlayers` - Yahoo player pages
- `importLeague`, `importTeam` - Import specific data
- `clearDatabase`, `dropDatabase`, `createDatabase`, `initDatabase`

**Queries:**
- `downloadSeasonsResult`, `downloadSeasonsProgress`
- `downloadYahooPlayersResult`, `downloadYahooPlayersProgress`
- `nhlConferences`, `nhlDivisions`, `nhlTeams`, `nhlTeam`

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

## References

- [Yahoo Fantasy Sports API Guide](https://developer.yahoo.com/fantasysports/guide/)
- [OAuth2 Example](https://stackoverflow.com/questions/48255130/yahoo-fantasy-sports-example-using-oauth2)
