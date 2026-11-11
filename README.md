# PuckDB

PuckDB is a self-hosted NHL and Yahoo Fantasy Hockey data platform. It keeps
the original API responses in a local cache, imports normalized data into
PostgreSQL, and serves the result through GraphQL and the Model Context
Protocol (MCP). Temporal makes the long-running download, import, draft, news,
and simulation jobs observable and resumable.

PuckDB provides:

- NHL schedules, rosters, box scores, play-by-play, shifts, player totals,
  standings, and NHL Edge tracking data
- Yahoo leagues, teams, rosters, matchups, drafts, transactions, rules, and
  player eligibility
- fantasy-draft projections, news adjustments, rankings, recommendations, and
  a live draft board
- GraphQL queries and workflow controls
- read-only MCP tools for NHL, Yahoo, and player-news data
- Prometheus metrics for the API, workers, cache, Redis, and PostgreSQL
- optional LLM-backed hockey chat and historical fantasy-pool simulations

## Architecture

```text
NHL API ─────┐                         ┌─ PostgreSQL ── GraphQL / MCP
             ├─ Temporal workflows ────┤
Yahoo API ───┘        │                └─ local response cache
                      │
                    Redis
              OAuth tokens and locks
```

The API starts and observes workflows. Workers fetch and import data, writing
raw responses to the file cache and queryable records to PostgreSQL. Redis
stores Yahoo OAuth tokens and coordinates shared work.

## Local setup

### Requirements

- Go 1.27 or newer
- Docker with Docker Compose v2

The Compose credentials below are for local development only.

### Start dependencies

```bash
docker compose up -d --wait pgbouncer redis temporal temporal-ui
```

This also starts PuckDB's PostgreSQL database and Temporal's PostgreSQL
database.

Create `.env` in the repository root:

```dotenv
PUCKDB_POSTGRES_PASSWORD=foo
PUCKDB_REDIS_PASSWORD=redis
PUCKDB_TEMPORAL_NAMESPACE=default
PUCKDB_DATA_PATH=.data/cache
```

The namespace override matches the namespace created by the bundled Temporal
environment.

### Build and initialize

```bash
go build -o puckdb .
./puckdb db migrate
```

Start the API and worker in separate terminals:

```bash
./puckdb api --instance puckdb
```

```bash
./puckdb worker --yahoo-seasons ""
```

The empty Yahoo seasons value gives you an NHL-only worker without Yahoo
credentials. In a third terminal, initialize reference data and import a
season:

```bash
./puckdb sync init
./puckdb sync seasons --season 2024
```

The season value is its starting year. A sync can download a large amount of
data, so keep the worker running until it finishes.

Open the GraphQL console at
[http://localhost:8787/graphql/](http://localhost:8787/graphql/) and inspect
workflows in [Temporal UI](http://localhost:8080).

## Yahoo Fantasy Hockey

Yahoo integration is optional. To enable it:

1. Create a Yahoo developer application whose redirect URI is
   `http://localhost:8787/yahoo/authenticated`.
2. Add these settings to `.env`:

   ```dotenv
   PUCKDB_YAHOO_OAUTH2_CLIENT_ID=your-client-id
   PUCKDB_YAHOO_OAUTH2_CLIENT_SECRET=your-client-secret
   PUCKDB_PUBLIC_URL=http://localhost:8787
   PUCKDB_YAHOO_SEASONS=yahoo-seasons.yaml
   ```

3. Create `yahoo-seasons.yaml`, keyed by season starting year:

   ```yaml
   2024:
     leagues:
       - league_id: 12345
         team_ids: [1, 2, 3]
   ```

4. Restart the API and worker without `--yahoo-seasons ""`, then visit
   [http://localhost:8787/yahoo/login](http://localhost:8787/yahoo/login).

The OAuth token is stored in Redis. `./puckdb yahoo signout` removes it.

## Interfaces

### GraphQL and draft board

The API serves:

| Path | Purpose |
| --- | --- |
| `/graphql/` | Interactive GraphQL console |
| `/graphql/query` | GraphQL POST endpoint |
| `/draft/` | Live fantasy-draft board |
| `/metrics` | API Prometheus metrics |

Run `./puckdb api --help` for authentication, TLS, draft, and server settings.
`--instance` is required and isolates users when several API instances share a
database. An instance can authenticate users through Authentik forward-auth
headers or verified Cloudflare Access JWTs.

### MCP

`puckdb mcp-server` exposes read-only tools over HTTP or stdio. The
`--mcp-toolsets` flag selects one or more toolsets:

| Toolset | Data |
| --- | --- |
| `nhl` | Public NHL players, games, stats, standings, Edge, and play-by-play |
| `yahoo` | Yahoo leagues, teams, rosters, standings, and drafts |
| `news` | News-source coverage and validated player-news events |

The default is `nhl`. For example:

```bash
./puckdb mcp-server --mcp-toolsets nhl --mcp-port 8790
./puckdb mcp-server --mcp-toolsets yahoo --mcp-port 8791 \
  --mcp-yahoo-leagues 465.l.1001
```

The HTTP endpoint has no built-in authentication. Keep Yahoo and news
instances on trusted networks, and use one instance per audience when the data
exposure differs.

### CLI

Run `./puckdb <command> --help` for flags and subcommands.

| Command | Purpose |
| --- | --- |
| `api` | Serve GraphQL, OAuth routes, the draft board, and API metrics |
| `worker` | Run Temporal workflows and activities |
| `sync` | Fetch and import all data or selected pipeline steps |
| `db` | Migrate, initialize, inspect, provision, or drop the database |
| `draft` | Inspect league rules, player pools, rankings, and draft sessions |
| `news` | Report news coverage and events or evaluate extraction models |
| `sim` | Create, inspect, and cancel historical fantasy-pool simulations |
| `mcp-server` | Serve read-only NHL, Yahoo, and news tools |
| `maurice` | Start the interactive LLM-backed hockey chat |
| `user` | Manage links between application users and Yahoo teams |
| `metrics` | Export cache, Redis, and database metrics |
| `redis` | Run Redis administration commands |
| `yahoo` | Manage Yahoo authorization and check season access |

`sync` accepts individual steps or groups:

```bash
./puckdb sync init
./puckdb sync seasons --from-season 2022 --to-season 2024
./puckdb sync players --season 2024
./puckdb sync edge --season 2024
```

With no step names, `sync` runs the complete pipeline.

## Configuration

Each command flag is also read from a `PUCKDB_` environment variable: uppercase
the flag name and replace hyphens with underscores. For example,
`--postgres-password` becomes `PUCKDB_POSTGRES_PASSWORD`; command-local flags
such as `sim create --config`, `sim tail --pool`, and `sim draft --reasoning`
use `PUCKDB_CONFIG`, `PUCKDB_POOL`, and `PUCKDB_REASONING`.

The `metrics --port` flag accepts both `PUCKDB_PORT` and the aliased
`PUCKDB_METRICS_PORT` configuration key. `PUCKDB_PORT` takes precedence when
both are set.

PuckDB loads `.env` from the current directory. Values are resolved in this
order:

1. command-line flags
2. process environment
3. `.env`
4. built-in defaults

Use command help as the configuration reference:

```bash
./puckdb api --help
./puckdb worker --help
./puckdb sync --help
```

## Default ports

| Service | Address |
| --- | --- |
| API | `http://localhost:8787` |
| Worker metrics | `http://localhost:8788/metrics` |
| Collector metrics | `http://localhost:8789/metrics` |
| MCP HTTP server | `http://localhost:8790/mcp` |
| Temporal | `localhost:7233` |
| Temporal UI | `http://localhost:8080` |
| PostgreSQL through PgBouncer | `localhost:5432` |
| Redis | `localhost:6379` |

## Development

Run commands from the repository root. The local CI checks are:

```bash
gofmt -l .
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
go build ./...
go test ./...
```

Database-backed tests use `PUCKDB_TEST_PG_URL` and skip when it is unset. Run
them one package at a time with `go test -p 1 ./...` when a test database is
available.

Simulation tests behind build tags need separate compile checks:

```bash
go vet -tags=integration ./...
go vet -tags=livellm ./...
```

After changing `internal/graph/schema.graphqls`, regenerate gqlgen output:

```bash
go tool gqlgen generate
```

Application packages live under `internal/`, command wiring under `cmd/`,
database migrations under `internal/database/migrations/`, and Temporal
workflows under `internal/worker/`.

## License

Copyright (C) 2026 Éric Spérano. PuckDB is licensed under the GNU General
Public License, version 3 or, at your option, any later version. See
[COPYRIGHT](COPYRIGHT) and [LICENSE](LICENSE).

The container image includes dependency license and notice files under
`/licenses`. Saved Yahoo Sports pages in
`internal/cache/testdata/yahoo-players/` are parser fixtures, remain Yahoo's
copyrighted material, and are not covered by the GPL.
