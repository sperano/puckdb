# Live-model sim smoke (Layer B) — operator runbook

Two ways to smoke-test the simulation's real LLM path against Ollama on
`ollama.local`. Neither runs in CI — these are pre-deploy checks an
operator runs when touching the LLM/agent-loop code
(`internal/worker/simulation/agent.go`, `manage_activity.go`, `draft_activity.go`,
`internal/llm/`). For the hermetic, CI-able guarantee see
`internal/worker/simulation/integration_smallseason_test.go` (Layer A).

## Option 1 — `TestLiveSmallSeasonOllama` (hermetic data, real model)

Runs the full `SimPoolWorkflow` on a synthetic 4-team / 7-day season with
4 real `provider: ollama` agents. Liveness assertions only: the pool
completes, every (agent, day) has a turn, no turn errored, and the
tool-call outcome distribution is logged (that's the quality signal to
eyeball — a healthy small model is mostly `accepted` with a scattering of
`validation_rejected`).

### Prerequisites

- Dedicated Postgres + Redis (disposable containers work):

  ```bash
  docker run --rm -d --name puckdb-integtest-pg \
    -e POSTGRES_DB=puckdb_integration_test -e POSTGRES_USER=puckdb \
    -e POSTGRES_PASSWORD=foo -p 15432:5432 postgres:16-alpine
  docker run --rm -d --name puckdb-integtest-redis -p 16379:6379 redis:7-alpine
  ```

- Temporal comes free: the harness spins up an in-process DevServer
  (downloads the Temporal CLI ~50MB on first run; set
  `PUCKDB_TEST_TEMPORAL_HOSTPORT` to reuse an existing server instead).

- Ollama reachable with the model pulled. The test pings
  `{PUCKDB_TEST_OLLAMA_URL}/api/tags` and **skips** with a clear message
  if the host is down or the model missing.

### Run

```bash
cd <puckdb checkout>   # ws/puckdb or a wt/<branch>/puckdb worktree

PUCKDB_TEST_PG_URL="postgres://puckdb:foo@localhost:15432/puckdb_integration_test?sslmode=disable" \
PUCKDB_TEST_REDIS_ADDR="localhost:16379" \
PUCKDB_TEST_OLLAMA_URL="http://ollama.local:11434" \
go test -tags=livellm -count=1 -timeout 45m -v ./internal/worker/simulation/ -run TestLiveSmallSeasonOllama
```

Optional: `PUCKDB_TEST_OLLAMA_MODEL` (default `llama3.1:8b`).

### Expected runtime

Dominated by LLM latency: 4 team names + 16 draft picks + 28 daily turns
≈ 50+ completions. With `llama3.1:8b` on ollama-host expect **10–25 minutes**.
The test's internal workflow deadline is 30 minutes; pass `-timeout 45m`
to `go test` so the Go test binary doesn't give up first.

### Failure triage

On failure the test dumps every non-accepted tool call and errored turn
(`dumpTurnTelemetry`). Read that first — it distinguishes "model emitted
garbage arguments" (`parse_error`), "model made an illegal move"
(`validation_rejected`, non-fatal by design), and "the LLM call itself
failed" (turn status `errored`, e.g. timeouts — consider a bigger
`TimeoutSeconds` or a smaller model).

## Option 2 — `examples/poolsim-smoke.yaml` (real cluster data, real model)

The same 4-Ollama-agent shape against the actual `20242025` season in
prod. Compact roster, `maxSeasonDays: 30`.

```bash
./puckdb sim create --config examples/poolsim-smoke.yaml   # prints pool_id
./puckdb sim tail <pool-id>                       # follow the day loop
```

Network prerequisite for cluster-side workers: pfSense blocks the
cluster subnet from initiating into the LAN, so the worker pods need a
pass rule `192.0.2.10/24 → 192.0.2.10:11434/tcp` before Ollama
agents resolve (verified 2026-07-07). Runs whose worker is on the LAN
are unaffected. `ollama.local` is the canonical hostname — plain
`ollama.local` does not resolve.

## Picking a window

Pools accept optional `startDate` / `endDate` (YYYY-MM-DD, within the
season's standings range) — the day loop runs
`COALESCE(startDate, standings_start)` .. `COALESCE(endDate,
standings_end)`, with `maxSeasonDays` still capping the count. So
"simulate March 2025" against real data is just:

```yaml
season: 20242025
startDate: "2025-03-01"
endDate: "2025-03-31"
```

Useful for Layer B runs: pick a dense schedule stretch instead of the
early-October trickle.
