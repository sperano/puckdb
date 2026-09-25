# PuckDB

PuckDB is a self-hosted hockey data pipeline and query service. It downloads
NHL and Yahoo Fantasy Hockey data, keeps the raw responses in a local file
cache, imports normalized records into PostgreSQL, and exposes the result
through GraphQL and MCP. Temporal runs the long-lived fetch, import, and
simulation workflows.

## What it provides

- NHL schedules, rosters, boxscores, play-by-play, shifts, player totals, and
  NHL Edge tracking data
- Yahoo Fantasy leagues, teams, rosters, matchups, drafts, and transactions
- Resumable, observable data workflows backed by Temporal
- A GraphQL API for data queries and workflow control
- A read-only MCP server for hockey-data tools
- Prometheus metrics for the API, workers, cache, Redis, and PostgreSQL
- Optional AI-assisted hockey chat and fantasy-pool simulation

## Architecture

```text
NHL API ─────┐                         ┌─ PostgreSQL ── GraphQL / MCP
             ├─ Temporal workflows ────┤
Yahoo API ───┘        │                └─ file cache
                      │
                    Redis
              OAuth tokens and locks
```

The API starts and observes workflows. Task workers perform downloads and
imports, writing raw API responses to the file cache and queryable data to
PostgreSQL. Redis stores Yahoo OAuth tokens and coordinates shared operations.

## Local quick start

### Prerequisites

- Go 1.27 or newer
- Docker with Docker Compose v2

The bundled Compose services and credentials are intended for local
development only.

### 1. Start the core services

```bash
docker compose up -d --wait pgbouncer redis temporal temporal-ui
```

This also starts the PostgreSQL instances required by PuckDB and Temporal.

### 2. Configure PuckDB

Create `.env` in the repository root:

```dotenv
PUCKDB_POSTGRES_PASSWORD=foo
PUCKDB_REDIS_PASSWORD=redis
PUCKDB_TEMPORAL_NAMESPACE=default
PUCKDB_DATA_PATH=.data/cache
```

The namespace override is required for the bundled Temporal setup. Yahoo is
disabled explicitly in the worker command below, so no Yahoo credentials are
needed for an NHL-only installation.

### 3. Build and migrate

```bash
go build -o puckdb .
./puckdb db migrate
```

### 4. Start the application

Run the API and worker in separate terminals:

```bash
./puckdb api
```

```bash
./puckdb worker --yahoo-seasons ""
```

Initialize the NHL reference data from a third terminal:

```bash
./puckdb sync init
```

The GraphQL console is now available at
[http://localhost:8787/graphql/](http://localhost:8787/graphql/), and Temporal
workflows can be inspected at
[http://localhost:8080](http://localhost:8080).

To fetch and import a season, pass its starting year:

```bash
./puckdb sync seasons --season 2024
```

Downloads can be large. Restrict the season range while evaluating the
project, and keep the worker running until the sync command finishes.

## Yahoo Fantasy setup

Yahoo support is optional. To enable it:

1. Create a Yahoo developer application with
   `http://localhost:8787/yahoo/authenticated` as its redirect URI.
2. Add the following values to `.env`:

   ```dotenv
   PUCKDB_YAHOO_OAUTH2_CLIENT_ID=your-client-id
   PUCKDB_YAHOO_OAUTH2_CLIENT_SECRET=your-client-secret
   PUCKDB_PUBLIC_URL=http://localhost:8787
   PUCKDB_YAHOO_SEASONS=yahoo-seasons.yaml
   ```

3. Create `yahoo-seasons.yaml`. Top-level keys are season start years:

   ```yaml
   2024:
     leagues:
       - league_id: 12345
         team_ids: [1, 2, 3]
   ```

4. Restart the API and worker so they load the new environment values. Start
   the worker without `--yahoo-seasons ""`, then visit
   [http://localhost:8787/yahoo/login](http://localhost:8787/yahoo/login) to
   authorize the application.

The OAuth token is stored in Redis. Run `./puckdb yahoo signout` to remove it.

## CLI

Run `./puckdb <command> --help` for the complete flags and subcommands.

| Command | Purpose |
| --- | --- |
| `api` | Serve GraphQL, Yahoo OAuth routes, and API metrics |
| `worker` | Run Temporal workflows and activities |
| `sync [steps...]` | Fetch and import all data or selected workflow steps |
| `db` | Migrate, provision, initialize, inspect, or drop the database |
| `redis` | Redis administration |
| `metrics` | Export cache, Redis, and database metrics |
| `mcp-server` | Serve curated read-only data tools over HTTP or stdio |
| `maurice` | Start the interactive AI hockey chat REPL |
| `sim` | Create and operate fantasy-pool simulations |
| `yahoo` | Yahoo account operations |

`sync` accepts individual steps or groups. Common examples:

```bash
./puckdb sync init
./puckdb sync seasons --from-season 2022 --to-season 2024
./puckdb sync players --season 2024
./puckdb sync edge --season 2024
```

Running `./puckdb sync` without step names executes the full pipeline.

## Services and ports

| Service | Default address | Notes |
| --- | --- | --- |
| PuckDB API | `http://localhost:8787` | Home page, OAuth routes, and `/metrics` |
| GraphQL console | `http://localhost:8787/graphql/` | Interactive query UI |
| GraphQL endpoint | `http://localhost:8787/graphql/query` | POST endpoint used by the CLI |
| Worker metrics | `http://localhost:8788/metrics` | Workflow and activity metrics |
| Collector metrics | `http://localhost:8789/metrics` | Started with `puckdb metrics` |
| MCP server | `http://localhost:8790/mcp` | Started with `puckdb mcp-server` |
| Temporal | `localhost:7233` | Workflow service |
| Temporal UI | `http://localhost:8080` | Workflow inspection |
| PostgreSQL | `localhost:5432` | Exposed through PgBouncer |
| Redis | `localhost:6379` | Password `redis` in local Compose |

Grafana (`localhost:3001`) and a restricted PostgreSQL MCP server
(`localhost:8000`) are also defined in `docker-compose.yaml`, but they are not
required for the quick start.

## Configuration

Every CLI flag can be supplied as a `PUCKDB_` environment variable: uppercase
the flag and replace hyphens with underscores. For example,
`--postgres-password` becomes `PUCKDB_POSTGRES_PASSWORD`.

PuckDB loads `.env` from the current working directory. Precedence is:

1. Explicit CLI flags
2. Process environment variables
3. Values in `.env`
4. Built-in defaults

Use command help to discover the available settings:

```bash
./puckdb api --help
./puckdb worker --help
./puckdb sync --help
```

## Development

Run commands from the repository root. The local CI-equivalent checks are:

```bash
gofmt -l .
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
go build ./...
go test ./...
```

Database-backed tests use `PUCKDB_TEST_PG_URL` and skip when it is unset.

The simulation integration tests in `internal/worker/simulation/` sit behind
the `integration` and `livellm` build tags, so `go test ./...` does not compile
them. Vet them explicitly to catch compile errors:

```bash
go vet -tags=integration ./...
go vet -tags=livellm ./...
```

Running them needs PostgreSQL, Redis, and (for `livellm`) an Ollama host; see
[the live-model smoke runbook](docs/sim-livellm-smoke.md).

After changing a GraphQL schema, regenerate the gqlgen output:

```bash
go tool gqlgen generate
```

Useful project references:

- [Workflow design](docs/workflows.md)
- [Fetch-seasons workflow](docs/fetch-seasons-workflow.md)
- [Migration recovery](docs/migration-recovery.md)
- [NHL Edge API notes](docs/nhl-edge-api.md)

Application packages live under `internal/`; command wiring lives in `cmd/`.
The GraphQL schemas are in `internal/graph/`, database migrations are in
`internal/database/migrations/`, and Temporal workflows are in
`internal/worker/`.
