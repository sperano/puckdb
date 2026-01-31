# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PuckDB is a Go application that imports and manages NHL hockey data and Yahoo Fantasy Sports data. It downloads data from the NHL API and Yahoo Fantasy Sports API, caches it locally as files, and stores processed data in PostgreSQL. Temporal handles workflow orchestration for long-running import jobs.

## Common Commands

### Build and Run
```bash
go build -o puckdb .                 # Build the binary
./puckdb api                         # Run as HTTP/GraphQL server (port 8080)
./puckdb worker                      # Run as Temporal worker (metrics on port 8788)
./puckdb download                    # Trigger download workflow via GraphQL API
./puckdb check cache                 # Verify cache completeness
./puckdb info                        # Display configuration info
```

### Testing
```bash
go test ./...                        # Run all tests
go test -cover ./...                 # Run tests with coverage
go test -v ./cache/...               # Run tests for a specific package
```

### Infrastructure
```bash
docker-compose up                    # Start Postgres, Redis, Temporal
```

### GraphQL Code Generation
```bash
go run github.com/99designs/gqlgen generate
```
Schema is at `graph/schema.graphqls`. Generated code goes to `graph/generated/` and `graph/model/`.

## Architecture

### CLI Commands (cmd/)
- **api** - HTTP server with GraphQL at `/graphql`, playground at `/graphql/`, and Yahoo OAuth2 at `/yahoo/*`
- **worker** - Temporal worker that processes download/import workflows; exposes `/metrics` endpoint
- **info** - Displays current configuration
- **metrics** - Exposes Prometheus metrics for cache, Redis, and database
- **cache-check** - Verifies cache file completeness against expected counts from NHL API
- **db** - Database operations
  - **db init** - Database initialization (creates tables, seeds NHL data)
  - **db drop** - Database cleanup (drops tables)
  - **db provision** - Creates database and user on shared PostgreSQL (uses Redis lock)
- **workflow** (alias: **wf**) - Workflow operations
  - **workflow download** - Triggers download workflows via GraphQL and monitors progress
  - **workflow cancel** - Cancels running Temporal workflows
- **yahoo** - Yahoo OAuth operations
  - **yahoo signout** - Clears OAuth2 token from Redis

Uses Cobra for CLI, Viper for configuration, and pflags for flags.

### Core Packages
- **worker/** - Temporal workflows and activities for downloading/importing data
  - Active workflows: `DownloadSeasonsWorkflow`, `DownloadYahooPlayersWorkflow`, `DownloadRosterForTeamWorkflow`, `DownloadTeamSummariesForTeamWorkflow`, `EnrichPlayersWorkflow`
  - Task queue name: `puckdb-tasks`
- **graph/** - GraphQL resolvers and schema (gqlgen)
- **database/** - GORM models and queries for PostgreSQL
- **sqlcdb/** - sqlc-generated type-safe SQL queries
- **cache/** - File-based caching and XML/JSON parsing for API responses
- **redis/** - Redis client for OAuth2 token cache and player data cache
- **config/** - Configuration flags, seasons config parsing from YAML
- **http/** - HTTP client with logging, Yahoo API URL builders
- **metrics/** - Prometheus metrics collection and HTTP server
- **temporal/** - Temporal client configuration
- **auth/** - Authentication context utilities
- **date/** - Date range and timestamp utilities

### Configuration
- Flags defined in `config/flags.go` with `Init*Flag` and `Bind*Flags` pattern
- Season configuration loaded from `seasons.yaml` (YAML file with start/end dates, game keys, league IDs, team IDs)
- OAuth2 tokens cached in Redis
- Environment variables supported via Viper

### Flag Conventions
**All CLI flags must follow these rules:**
1. **Flag names** must be defined as constants in `config/flags.go` (e.g., `FlagAPIPort = "api-port"`)
2. **Default values** must be defined as constants in `config/defaults.go` (e.g., `DefaultAPIPort = 8787`)
3. **Never use magic numbers or string literals** as default values in flag definitions
4. Each flag should have an `Init*Flag(flags *flag.FlagSet)` function that registers the flag with its default constant
5. Each flag should have a `Bind*Flag(flags *flag.FlagSet) error` function for viper binding
6. Cmd files should only call `config.Init*Flag()` and `config.Bind*Flag()` - never define flags locally

### Data Flow
1. User authenticates via Yahoo OAuth2 (`/yahoo/login` → stored in Redis)
2. GraphQL mutations or CLI commands trigger Temporal workflows
3. Workers download data from NHL API and Yahoo API, store as cached files
4. Workers parse cached files and store structured data in PostgreSQL
5. GraphQL queries serve data from PostgreSQL

### GraphQL API
**Mutations:**
- `downloadSeasons(input)` / `cancelDownloadSeasons` - Download NHL and Yahoo season data
- `downloadYahooPlayers` / `cancelDownloadYahooPlayers` - Download Yahoo player pages
- `importLeague`, `importTeam` - Import specific league/team data
- `clearDatabase`, `dropDatabase`, `createDatabase`, `initDatabase` - DB management

**Queries:**
- `downloadSeasonsResult`, `downloadSeasonsProgress` - Workflow status
- `downloadYahooPlayersResult`, `downloadYahooPlayersProgress` - Workflow status
- `nhlConferences`, `nhlDivisions`, `nhlTeams`, `nhlTeam` - NHL reference data

### External Services
- **PostgreSQL** (port 5432): Main data store with GORM ORM (user: puckdb, password: foo, db: puckdb)
- **Redis** (port 6379): OAuth2 token cache, player data cache (password: redis)
- **Temporal** (port 7233): Workflow orchestration
- **Temporal UI** (port 8080): Workflow monitoring

### Metrics

The worker exposes Prometheus metrics at `/metrics` (default port 8788, configurable via `--worker-port`). A health check endpoint is available at `/health`.

**Filesystem Metrics**
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_fs_operation_duration_seconds` | Histogram | operation, file_type | Duration of filesystem operations |
| `puckdb_fs_bytes` | Histogram | operation, file_type | Size of read/write operations in bytes |

**HTTP Metrics**
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_http_request_duration_seconds` | Histogram | api, status_code | Duration of HTTP requests to external APIs |
| `puckdb_http_response_bytes` | Histogram | api | Size of HTTP response bodies |

**Download Workflow Metrics**
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_download_total` | Counter | file_type, result | Download operations count (result: hit/miss/error) |

**Temporal Activity Metrics**
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `puckdb_activity_duration_seconds` | Histogram | activity | Duration of Temporal activities |

### Yahoo OAuth2 References

- [Fantasy Sports API Guide](https://developer.yahoo.com/fantasysports/guide/)
- [OAuth2 Example](https://stackoverflow.com/questions/48255130/yahoo-fantasy-sports-example-using-oauth2)
