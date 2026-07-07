# Sim Navigation: How It's Supposed to Work, and Why It Doesn't Serve You

*Written 2026-06-01 after debugging pool 14, where `sim step` and `sim continue` both appeared to do nothing.*

## The three concepts

### 1. Control state (`controlState`)

Lives in the running `SimPoolWorkflow`'s memory (`worker/simulation/workflow.go:721`). Three fields:

| Field | Type | What it tracks |
|---|---|---|
| `mode` | `ModeRunning` / `ModePaused` / `ModeStepping` | What to do at the next gate |
| `pendingSteps` | int | How many step signals are queued |
| `pauseAt` | `never` / `each_event` / `each_phase` / `at_end` | Which kinds of gates block |

Mutated by signals:
- `advance` → `pendingSteps++` and `mode = ModeStepping`
- `auto_advance` → `mode = ModeRunning`, `pendingSteps = 0`
- `pause` → `mode = ModePaused`, `pendingSteps = 0`
- `breakpoint` → updates `pauseAt` to a new enum value

State survives ContinueAsNew via the workflow input (`SimPoolWorkflowInput.Mode` / `PendingSteps`).

### 2. Gates (`passGate` call sites)

Points in the workflow code where execution **may** block. Three boundary types:

| Boundary | Where it fires |
|---|---|
| `EventBoundaryEvent` | After each agent-driven LLM event: team_name pick, draft pick, daily roster turn |
| `EventBoundaryPhase` | At phase transitions: end of team_name phase, end of each draft round, end of each season day |
| `EventBoundaryEnd` | Sim completion (reserved; not currently called) |

At each gate, `passGate` consults the control state and decides: block, or pass through. The decision logic is in `workflow.go:763`:

```
ModeRunning  + ShouldPauseAt(pauseAt, boundary) → pause (transition to ModePaused, block)
ModeStepping + pendingSteps > 0                 → decrement, continue
ModeStepping + pendingSteps == 0                → pause (just consumed last step)
ModePaused                                      → pause unconditionally
```

When blocking, the workflow does `for cs.mode == ModePaused { sel.Select(ctx) }` — i.e., wait for a signal to mutate mode.

### 3. Activity ledger

Temporal's record of `ActivityTaskScheduled` / `Started` / `Completed` events — i.e., **work the worker actually did**: LLM calls, DB writes, etc.

**Key point: gates are NOT activities.** A gate is just `sel.Select(ctx)` waiting for a signal — purely an in-workflow await, invisible to the worker, no Temporal event for "we paused at a gate." The only way you know the workflow paused is by querying the workflow's `QuerySummary` handler or by inferring from "no new `ActivityTaskScheduled` events after the last signal."

## What just bit us with pool 14

Mental model: `step` = "make stuff happen in the worker."
System's contract: `step` = "advance past one gate."

For team_name phase, those mean different things, because:

1. The workflow spawned 5 `PickTeamName` activities **in parallel** (one `workflow.ExecuteActivity` per agent, no `Get` between them).
2. All 5 completed in a burst — that's why `sim_agent_turns` showed 5 rows with `status=ok` *before* any step signal was sent.
3. The workflow is now in this loop (`workflow.go:418-433`):
   ```go
   for i, f := range futures {
       f.Get(ctx, &res)             // instant — activity already done
       passGate(EventBoundaryEvent) // ← blocks here
   }
   passGate(EventBoundaryPhase)     // ← then blocks here
   ```
4. Each `step` exits one `passGate` and immediately walks into the next. **No activity gets scheduled**, because PickTeamName is done. So the worker is silent.
5. `continue` (auto_advance) with `pause_at=each_event` is ALSO useless: `ModeRunning` still consults `ShouldPauseAt`, which says "yes, pause" for every event boundary. Workflow advances one gate, re-pauses. Same observable outcome as `step`.

Workflow history confirms it: both signals arrived, both `WorkflowTaskCompleted` events fired, no `ActivityTaskScheduled` followed either.

## Why the design is overbuilt for the actual use case

The debugger-style runtime navigation made sense in theory — it's how you'd inspect a long-running production sim. But it carries real costs:

- **Concept count.** Four enum values (pause_at) × three modes (control state) × three signals × four CLI commands. ~14 distinct vocabulary items for one feature.
- **The parallel-completion gotcha.** Per-event gates on parallel-completion paths are nonsense; we just lived through it. Daily turns are parallel too, so the same trap waits at every season day.
- **State machine debuggability.** Predicting "what will `step` do?" requires modeling `mode + pendingSteps + pauseAt + which gate is next + whether the next activity is parallel-pending or already-done`. Five interacting variables for a UX you'd hit a handful of times.
- **Four sites computing the same display fallback** (`displayName` in three Go locations + an SQL `COALESCE` in sim_tail). Symptom of the design touching many surfaces.
- **A test for "is this complexity worth it":** can you describe what `step` does in one sentence without mentioning "boundary," "mode," or "pending"? Currently we can't.

For the stated goal — **"baby steps, debug easily with small samples"** — none of this complexity buys anything. The user doesn't want to interactively walk through 90 draft picks; they want a small enough sim that they can run it to completion in a minute and read the resulting telemetry.

## The proposed alternative: config-time scoping

**Config flags at pool creation:**
- `draftRounds: 2` *(already exists — just lower the value for small samples)*
- `stopAfter: never | team_name | draft | season` — workflow exits cleanly when reaching the configured phase, writes `status=complete`, and stops. Default `never` = run to season end.
- Optional: `maxSeasonDays: N` — cap the season at N days instead of running to season end. Useful for a "10-day sample" debug.

**Runtime controls collapse to two:**
- `puckdb sim cancel <id>` — already exists, kill it
- *(optional)* `puckdb sim pause <id>` — emergency stop if needed, but probably you'd just cancel and recreate

**Everything else goes away:**
- Drop `pause_at` column, drop the four enum values
- Drop `controlState`, drop `ControlMode`, drop `EventBoundary`, drop `passGate`
- Drop `SignalAdvance`, `SignalAutoAdvance`, `SignalBreakpoint`
- Drop CLI commands `step`, `continue`, `break`
- Drop the workflow's signal selector (only `pauseCh` survives, if we keep emergency-stop)

### What's lost

The (theoretical) ability to inspect a long-running sim mid-execution. You can still tail telemetry to watch what happened, but you can't pause mid-execution to look at one specific pick.

### What's gained

- The workflow becomes "fire-and-forget within configured scope."
- ~200 lines of state-machine code goes away.
- The mental model is one sentence: *"configure how much sim you want at creation; it runs to that point."*

## Recommendation

Do what was sketched. Add `stopAfter` (and `maxSeasonDays` if useful), let `draftRounds` shrink for small samples, rip out the rest. The runtime navigation was an over-engineered answer to a question that wasn't actually being asked.

### Side benefits

- `stopAfter` also gives a natural way to write integration tests: `stopAfter: draft` + small `draftRounds` = a hermetic, deterministic small-scope sim that completes in seconds. Right now the integration tests either short-circuit with mocks or run a full season — both awkward.
- The instinct that led to this complexity was treating "running sim" as a long-running process you'd want to debug interactively. Reality: simulations are reproducible — you can always re-create the pool with different scope and re-run. Reproducibility makes the runtime debugger pattern unnecessary; the build-and-rerun pattern is faster.
- The CLI surface shrinks from 8 commands to 4: `create`, `status`, `tail`, `cancel`.

## Refactor scope estimate

- Migration 000022: drop `pause_at`, add `stop_after` + `max_season_days`.
- ~250 lines deleted from `worker/simulation/workflow.go`, `cmd/sim.go`, GraphQL schema + resolvers, and tests.
- Mechanical work. ~30–45 minutes including a full test pass.
