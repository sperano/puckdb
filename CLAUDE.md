# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PuckDB is a Go application that imports and manages fantasy hockey data from Yahoo's Fantasy Sports API. It stores data in PostgreSQL and uses Temporal for workflow orchestration.

## Common Commands

### Build and Run
```bash
go build -o puckdb .                 # Build the binary
./puckdb api                         # Run as HTTP/GraphQL server
./puckdb worker                      # Run as Temporal worker
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
- `api` - HTTP server with GraphQL endpoint at `/graphql` and Yahoo OAuth2 at `/yahoo/*`
- `worker` - Temporal worker that processes import workflows
- `info` - Displays current configuration
- `drop` - Database cleanup
- `init` - Database initialization

Uses Cobra for CLI, Viper for configuration, and pflags for flags.

### Core Packages
- **worker/** - Temporal workflows and activities for importing data from Yahoo
  - Key workflows: `ImportEverythingWorkflow`, `ImportLeagueWorkflow`, `ImportGamesForSeasonWorkflow`
  - Task queue name: `puckdb-tasks`
- **graph/** - GraphQL resolvers and schema. Resolver methods are in `resolver.go` and `schema.resolvers.go`
- **database/** - GORM models and queries for PostgreSQL
- **cache/** - File-based caching and XML parsing for Yahoo API responses
- **redis/** - OAuth2 token caching in Redis
- **config/** - Configuration flags, seasons config parsing from YAML
- **http/** - HTTP client, Yahoo API URL builders, and request logging

### Configuration
- Flags defined in `config/flags.go` with `Init*Flag` and `Bind*Flags` pattern
- Season configuration loaded from `seasons.yaml` (YAML file with start/end dates, game keys, league IDs, team IDs)
- OAuth2 tokens cached in Redis

### Data Flow
1. User authenticates via Yahoo OAuth2 (stored in Redis)
2. GraphQL mutations trigger Temporal workflows
3. Workers download data from Yahoo API, parse XML, store in PostgreSQL
4. GraphQL queries serve data from PostgreSQL

### External Services
- **PostgreSQL** (port 5432): Main data store with GORM ORM
- **Redis** (port 6379): OAuth2 token cache
- **Temporal** (port 7233): Workflow orchestration
- **Temporal UI** (port 8080): Workflow monitoring

### Database

check docker-compose.yaml for the parameters used to connect to the local database

### Yahoo Oauth2 References

[GUIDE](https://developer.yahoo.com/fantasysports/guide/)
[OAUTH2](https://stackoverflow.com/questions/48255130/yahoo-fantasy-sports-example-using-oauth2)

