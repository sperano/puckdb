# PuckDB

A Go application for importing and managing NHL hockey data and Yahoo Fantasy Sports data. Downloads from the NHL API and Yahoo Fantasy Sports API, caches responses locally, and stores processed data in PostgreSQL. Temporal handles workflow orchestration for long-running import jobs.

## Features

- Download NHL schedules, boxscores, rosters, and player stats
- Import Yahoo Fantasy Sports league and team data via OAuth2
- File-based caching to avoid redundant API calls
- GraphQL API for querying data and triggering workflows
- Prometheus metrics for monitoring

## Quick Start

```bash
# Start infrastructure
docker-compose up -d

# Build
go build -o puckdb .

# Initialize database
./puckdb db init

# Run the API server (GraphQL at localhost:8080/graphql)
./puckdb api

# In another terminal, run the worker
./puckdb worker

# Trigger a download
./puckdb wf download
```

## Commands

| Command | Description |
|---------|-------------|
| `api` | HTTP server with GraphQL endpoint and Yahoo OAuth2 |
| `worker` | Temporal worker for download/import workflows |
| `info` | Display configuration |
| `metrics` | Prometheus metrics server for cache/Redis/database stats |
| `cache-check` | Verify cache completeness |
| `db init` | Initialize database (migrations + seed NHL data) |
| `db drop` | Drop all database tables |
| `db provision` | Create database and user on shared PostgreSQL |
| `wf download` | Trigger download workflow via GraphQL |
| `wf cancel` | Cancel running Temporal workflows |
| `yahoo signout` | Clear OAuth2 token from Redis |

`wf` is an alias for `workflow`.

## Infrastructure

- **PostgreSQL** (5432) - Main data store
- **Redis** (6379) - OAuth2 token cache
- **Temporal** (7233) - Workflow orchestration
- **Temporal UI** (8080) - Workflow monitoring

## Dependencies

- [nhl-api-go](https://github.com/sperano/nhl-api-go) - Go client library for the NHL Stats API
