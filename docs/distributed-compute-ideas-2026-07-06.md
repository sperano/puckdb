# Ideas: Making the Distributed Design Shine

**Date:** 2026-07-06
**Status:** Ideas / discussion
**Scope:** `worker/`, `worker/simulation/`, hollingsworth helm values, Grafana

## The headroom, in numbers

- **16 worker pods × 75 activity slots = ~1,200 concurrent activity slots**
  (`worker.replicaCount: 16`, `maxActivityExecution: 75`,
  `values-hollingsworth.yaml:118-131`). Fixed fleet, HPA defined but disabled.
- Today that fleet mostly runs one ingest pipeline at a time (season fan-out
  capped at `maxSeasonConcurrency: 15`, day fan-out at 20) or a handful of
  sim pools. Most slots idle most of the time.
- **Within a sim pool, the expensive work is serialized:** the daily loop runs
  `ManageRoster` agent-by-agent (`workflow.go:836-861`). Only the team-name
  phase is parallel. The parallelism knob today is "run more pools."
- **Real ceilings to respect:** 12 Postgres conns/pod (≈192 total behind
  PgBouncer) vs 75 slots/pod; a **single Ollama host** — `ollama.local`,
  the box the poolsim YAMLs target (the in-cluster `ollama` Service in
  `helm/ollama/` previously pointed at a stale IP for a different Mac,
  ollama-host, whose Ollama binds to localhost; as of 2026-07-06 the chart is an
  ExternalName to `ollama.local`, but **cluster pods still get connection
  refused** — ollama-host's Ollama needs `OLLAMA_HOST=0.0.0.0` before any
  cluster-side Ollama sim works); NHL API politeness (no client-side limiter
  exists, concurrency-bounded only).

The ideas below are ordered by fun-per-effort, each grounded in an existing
mechanism.

---

## 1. The Model League — a standing LLM tournament (flagship)

The sim is already multi-pool-concurrent: each `SimPoolWorkflow` is an
independent top-level workflow, and the fleet can host dozens. Nobody is
exploiting that.

**Idea:** a `SimTournamentWorkflow` parent that fans out `SimPoolWorkflow`
children (same `RunWorkerPool` pattern as `FetchSeasonsWorkflow` →
`FetchSeasonWorkflow`, `fetch_seasons.go:31-70`):

- **Round-robin:** every model pairing (sonnet vs haiku vs llama3.1 vs qwen…)
  across K pools with rotated draft positions and seasons, so results aren't
  one-draft luck.
- **Strategy ablation:** same model, 6 different strategy prompts, 10 pools —
  does the "punt goalies" strategy actually work, or does the model ignore it?
  (Pairs with the strategy-adherence stat in `fun-stats-analysis-2026-07-06.md`.)
- **Season sweep:** identical pool config replayed across 10 historical
  seasons in parallel — is a model's win robust or era-dependent?
- Aggregation: a `sim_tournaments` table + cross-pool standings (mean roto
  points, cost-per-point per model from the existing telemetry). Output: a
  league table that updates live in Grafana.

Cheap to build: the child-workflow machinery, the pool lifecycle, and the
telemetry all exist. The parent is mostly bookkeeping + an aggregate query.
Cost control: all-Ollama tournaments are $0; mixed tournaments reuse the
per-pool cost cap.

## 2. Parallelize the daily agent turns (optimistic concurrency)

The sequential daily loop looks mandatory but mostly isn't: each agent's turn
already runs against a **morning snapshot** with **commit-time conflict
revision** (`reviseActionsForCommitConflicts`, `manage_activity.go:723`) that
handles "someone else took that free agent first."

**Idea:** run `BuildManageRosterContext` + `ManageRoster` (the LLM inference,
the slow part) for all agents **in parallel futures**, then commit the results
**sequentially in the same randomized fairness order**, letting the existing
commit-time revision arbitrate FA contention. Semantics shift slightly (an
agent no longer sees the same-day adds of earlier-processed agents in its
prompt — it already doesn't see them at context-build time today unless it's
late in the order), so make it a per-pool option: `parallelTurns: true`.

Payoff: a 12-agent pool's day drops from 12 sequential LLM calls to
~max(one call). Combined with idea 3 this is the difference between a season
sim taking hours vs minutes. Caveat: this multiplies concurrent load on the
LLM host, which today is one Mac — hence idea 3.

## 3. Scale the local-model tier

The Ollama Service is an ExternalName alias for one external host
(`ollama.local`). Options, in increasing ambition:

- **Multiple hosts:** switch the chart back to a headless Service + Endpoints
  listing several host IPs for kube-native round-robin (ExternalName can only
  alias one name), or give each host its own ExternalName Service and route
  per-agent via `apiBase`. (Caveat: each host must have the model pulled;
  cold-start pulls are slow.)
- **An LLM gateway in-cluster** (LiteLLM or a tiny Go proxy): model-aware
  routing (send llama3.1 to hosts that have it), queueing, per-host
  concurrency caps, retries, and one `apiBase` for all sim configs instead of
  hardcoded host URLs in YAML. The instrumented client
  (`puckdb_sim_llm_call_duration_seconds`) already gives per-model latency to
  drive routing decisions.
- **In-cluster inference on the SSD nodes** for small models (the helm chart
  for ollama exists; today it only wraps the external Mac). BOINC already
  runs in this cluster — the nodes have spare cycles.

## 4. Distributed derived-stats compute (feeds the fun-stats plan)

The heavy tiers of `fun-stats-analysis-2026-07-06.md` — linemate inference
over 14.5M shifts, xG rollups over 7.8M play_events — are embarrassingly
parallel per (season, game). This is exactly the shape the ingest pipeline
already handles:

- `ComputeDerivedStatsWorkflow` parent → per-season children →
  `RunWorkerPool` over games, writing rollup tables. 1,200 slots chew through
  65K games quickly; the binding constraint is the 12-conns/pod DB pool, so
  batch writes per activity (existing batch-helper patterns from the cleanup
  plan apply).
- Nightly Temporal **cron** workflow refreshes recent seasons; full backfills
  are a one-off tournament for the fleet.

## 5. Monte Carlo baseline managers

LLM managers need a baseline to be meaningful. A **non-LLM agent** (random
legal moves; greedy by last-7-days points; static draft-and-hold) costs
nothing per turn and needs no LLM host.

**Idea:** implement 2-3 scripted `AgentFactory` strategies as first-class
providers (`provider: scripted, model: greedy-last7`). Then run **thousands**
of all-scripted pools across the fleet (pure CPU + Postgres) to build the
replacement-level distribution: "a random manager finishes with 21.3 ± 3.1
roto points in this format." Every LLM result then gets a percentile.
This also stress-tests the sim engine itself far beyond what LLM-speed pools
can (finds race conditions, waiver edge cases) — cheap chaos testing.

## 6. Observability: make the fleet visible

The metrics exist; the dashboards don't.

- **Simulation dashboard** (gap: three `puckdb_sim_*` metrics, zero panels):
  live pool standings race, LLM latency by provider/model, failures, day
  duration, cost burn vs cap, tool-call outcome rates (needs the telemetry
  exporter from the fun-stats plan Phase 1).
- **Fleet saturation panel:** activity-slot utilization vs the 16×75 ceiling,
  task-queue backlog (Temporal server metrics are already scraped —
  `temporal.json` dashboard has pollers/shards), DB-conn usage vs the
  192-conn ceiling. This is the panel that tells you when ideas 1/4/5 are
  actually saturating the cluster (satisfying to watch).
- **Tournament "race" dashboard** for idea 1 — cross-pool league table.

## 7. Right-size the knobs (small, do alongside)

- `maxWorkflowExecution` is unset on the tasks worker (SDK default) — set it
  explicitly.
- Enable the worker **HPA** (already templated, `hpa-worker.yaml`) or, better,
  scale on Temporal task-queue depth via KEDA — the fleet is fixed at 16
  regardless of load today.
- The DB-conns-per-pod (12) vs activity-slots (75) mismatch: either raise
  pooled conns for compute-heavy workloads or cap DB-touching activity
  concurrency separately from LLM-bound activities (two activity option
  profiles already exist — a third "db-heavy" profile is natural).
- Add a client-side NHL API rate limiter before any idea that widens ingest
  fan-out (there is none today; politeness is currently structural).

## Suggested sequencing

1. **Observability first** (idea 6, fleet + sim dashboards) — every other idea
   becomes measurable and demo-able.
2. **Scripted agents + Monte Carlo baselines** (idea 5) — no LLM-host
   dependency, exercises the fleet immediately, gives the science a control
   group.
3. **Model League tournament workflow** (idea 1) — the flagship; starts
   all-Ollama once idea 3's first step (a second Ollama host) lands.
4. **Parallel daily turns** (idea 2) behind a pool flag, once the LLM tier can
   absorb it.
5. **Derived-stats compute** (idea 4) when the fun-stats plan reaches its
   heavy tiers.
