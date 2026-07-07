# PuckDB Code Review & Improvement Plan — 2026-07-06

Scope: whole-app review of `ws/puckdb` on branch `feature/simulation` (~40k hand-written
Go LOC). Generated code (`sqlcdb/`, gqlgen `*_gen.go`) excluded. Review performed by 8
parallel reviewers across: simulation workflow/activities/domain/LLM layers, non-sim
worker, graph resolvers, cmd CLI, and service packages (`cache`, `httpx`, `llm`, `maurice`,
`matching`, `store`, `mcpserver`, `config`, `metrics`, `core`).

**Model assignment key** (per project tiering):
- **opus** (`go-genius`): subtle concurrency, Temporal replay/retry/idempotency,
  transactional correctness, distributed-systems edge cases. A plausible-but-wrong fix is
  likely and expensive.
- **sonnet** (`go-expert`): routine refactor, dedup, idiom, naming, formatting. Fix shape
  is well understood.

Overall quality is **high**: Temporal determinism discipline, idempotency probes, `%w`
wrapping, and honest invariant-comments are the norm. The dangerous bugs are all
distributed-systems-shaped and few; most findings are mechanical cleanup.

---

## Tier 0 — Verifications (RESOLVED 2026-07-06)

Both gating questions investigated and closed. Outcomes fold into Tier 1/Tier 2 below.

### V1 — Cost cap semantics → **comparison is correct-as-designed; T1-B daily-loop flood is real (downgraded to sonnet)**

- The `current >= capUsd` check (`costcap.go:56`) is a **cumulative post-hoc soft ceiling**,
  and that is the *only* enforceable design: an LLM call's token cost is unknowable until the
  call is made, so "don't make a call that would exceed" cannot be computed pre-call. The
  code's "pre-flight" wording refers to *timing* (checked before billing), not prediction.
  **No change to the comparison.** The one-call overshoot is inherent and expected — worth a
  one-line comment on `checkCostCap` saying so, then close.
- **However T1-B is confirmed real and still open.** The **draft** loop breaks on
  `SkipReasonCostCapReached` (`workflow.go:498`, fixed in the prior review), but the **daily**
  loop (`processOneDay:836-861`) never inspects `res.SkipReason` — it only accumulates
  `res.CostUsd`. So after agent N trips the cap, agents N+1…K each still run
  `BuildManageRosterContext` + `ManageRoster`, each re-trips, each inserts a duplicate
  `cost_cap_reached` row + fires a redundant (idempotent, but wasteful) pause signal, all
  before `checkPause()` fires at day's end. **Fix = mirror the draft-loop break in
  `processOneDay`** (return/break as soon as `res.SkipReason == SkipReasonCostCapReached`).
  Reference pattern exists 340 lines up → **sonnet, effort S** (was listed opus/M).

### V2 — Waiver overlap → **possible but narrow; B4 downgraded to sonnet defensive hardening**

- `ProcessWaivers` runs with `defaultActivityOptions()` (30s StartToClose, **no
  HeartbeatTimeout**, 5 attempts) and is invoked with a blocking `.Get()` → **serial within a
  run**. Pool workflow IDs are unique (one execution per pool) and ContinueAsNew is serial →
  **no cross-run overlap**.
- The *only* overlap vector: a waiver transaction that runs past its 30s StartToClose, letting
  the server schedule attempt N+1 while attempt N is still committing. Waiver txns are tiny
  (a few claims), so this is unlikely; and `sim_rosters`' unique constraint `(pool_id,
  player_id)` turns any double-apply into a clean PK-conflict retry-failure, not silent
  corruption. Claim-status writes are already idempotent (query filters `status='pending'`).
- **B4 stays worth doing as cheap hardening** — move the priority read inside the `InTx`
  callback with a `SELECT … FOR UPDATE` variant so the read-modify-write is atomic — but it is
  **low severity, sonnet** (was opus). Not a merge blocker.

---

## Tier 1 — Correctness bugs (opus) — fix before further sim work

### T1-A · Temporal replay determinism — shared worker-pool Selector
- **`worker/shared/progress.go:276,335`** — `RunWorkerPoolWithIncrement` /
  `RunWorkerPoolMultiBar` rebuild the `workflow.Selector` each iteration by ranging over
  `active map[int]workflow.Future`. Map order is randomized; `Select` fires the
  first-registered ready branch, so when ≥2 activities are complete at one decision point
  the order of side-effecting `IncrementBarBy`→`Save` local-activity commands and the
  `firstErr` choice varies across replay → non-determinism error / wrong first error. This
  is the core fan-out primitive (ProcessPlayers, FetchSeasons, Yahoo, …).
  **Fix:** register futures with the Selector in ascending index order
  (`for i := 0; i < nextIndex; i++ { if f, ok := active[i]; ok {…} }`). Effort M.

### T1-B · Cost-cap daily loop — duplicate rows + pause storms *(→ sonnet, per V1)*
- **`worker/simulation/workflow.go:836-861`** — the per-agent daily loop does not inspect
  `res.SkipReason`; every remaining agent after the cap trips still runs
  `BuildManageRosterContext` + `ManageRoster`, each re-trips, inserts a `cost_cap_reached`
  row, and fires a redundant pause signal. `checkPause()` only fires after the full agent
  loop. **Fix:** break the per-agent loop on the first `SkipReasonCostCapReached` (mirror
  the draft loop's early return at `workflow.go:498`). Confirmed real by V1; downgraded to
  **sonnet, effort S** because the reference pattern already exists in the same file. The
  `current >= capUsd` comparison itself is correct-as-designed — do NOT add a next-call
  estimate (see V1).

### T1-C · LLM re-billing on post-completion flake
- **`worker/simulation/manage_activity.go:562-575`** — `commitDailyTurn` reads
  `record_full_messages` (static config) *after* the LLM already ran; a flake there
  discards a completed, paid-for turn and forces a full Temporal retry that re-bills the
  LLM (idempotency marker not yet written). **Fix:** fetch `record_full_messages` up-front
  with the rest of pool state. Effort M.

### T1-D · Cancel/ContinueAsNew defer misclassification
- **`worker/simulation/workflow.go:110-118`** — the cancel-cleanup defer writes
  `status='cancelled'` whenever `retErr != nil && ctx.Err() != nil`, but `retErr` also
  carries `ContinueAsNewError`. A cancel in the same decision window as a CAN would mark a
  continuing pool cancelled. **Fix:** exclude CAN/canceled explicitly
  (`!temporal.IsCanceledError && !workflow.IsContinueAsNewError`). Effort S.
- **`workflow.go:737-740`** — `RecordDayDuration` error fully discarded; comment claims a
  ctx-cancel check that the code doesn't do → one extra day of state mutation past cancel.
  **Fix:** re-check `ctx.Err()` immediately after. Effort S.

### T1-E · completePool completes an unstarted progress group
- **`worker/simulation/workflow.go:159,177` → `completePool` (931-933)** — StopAfter
  pre-season exits call `completePool`, which `CompleteGroup(ctx, 1)` (the Season group)
  though it was never `StartGroup`-ed → bogus "Season complete" event + reads an unstarted
  timer. **Fix:** status-only completion variant for pre-season stop paths. Effort M.

### T1-F · Lineup batch displacement can silently undo an earlier move
- **`worker/simulation/validate.go:260-276`** — `pickDisplacement` always evicts the
  lowest player_id occupant of the target slot, never excluding players placed by an
  *earlier move in the same `set_lineup` batch*. move[0]→D then move[1]→D-full can displace
  move[0]'s player back to BN with no error. **Fix:** track batch-placed IDs; prefer
  original occupants or reject with `ErrSlotCapacityExceeded`. Add table-driven tests
  across all `fromSlot` origins (also covers the under-tested BN-capacity accounting).
  Effort M.

### T1-G · cache/ trust-boundary bugs (download/import depends on these contracts)
- **`cache/gob_cache.go:200-204`** — a successful storage read is failed hard when the
  best-effort cache *write* errors. **Fix:** log write failure, return
  `obj, OriginFileSystem, nil`. Effort S.
- **`cache/progress.go:86-90`** — `pipe.Exec` error captured then ignored (empty `if`);
  Redis-down is indistinguishable from no-reports and returns `(partial, nil)`. **Fix:**
  return the error when not `redis.Nil` and result is empty. Effort S.
- **`cache/boxscore_players.go:62-67,104-108`** — `redis.Nil` not honored despite doc
  contract "returns nil slice if key missing"; a normal miss becomes a hard error.
  **Fix:** `if errors.Is(err, redis.Nil) { return nil, nil }`. Effort S.

### T1-H · maurice detached goroutine sharing service clients
- **`maurice/service.go:238`** — `go s.generateTitle(context.Background(), …)` reuses
  `s.llmClient`/`s.db` on a detached, unbounded goroutine ignoring shutdown; concurrent
  `Chat` calls race on non-guaranteed-threadsafe clients. **Fix:** bounded worker/errgroup
  with a `context.WithoutCancel(ctx)` derived timeout context; confirm client thread-safety.
  Effort M.

---

## Tier 2 — Correctness bugs, lower blast radius (opus unless noted)

| # | File:line | Problem | Fix | Effort | Model |
|---|-----------|---------|-----|--------|-------|
| B1 | `worker/yahoo/import_activities.go:657-671` | Matchups pagination `break`s on a missing week → silently under-imports later weeks when cache is non-contiguous; disagrees with fetcher's `continue`. | Iterate to `maxMatchupWeeks`, `continue` on absent. | S | opus |
| B2 | `worker/asset/activities.go:184-190` | Comment says ctx-cancel aborts batch; code stringifies it into a per-row `Err` and continues. | Return ctx error as batch error. | S | opus |
| B3 | `worker/nhl/import_boxscores.go:234-244,316-322` | Hand-rolled batch-error capture keeps only first error; partial commit reported as success. Re-implements `shared.ExecBatch`. | Use `shared.ExecBatch`. | M | opus |
| B4 | `worker/simulation/waivers_activity.go:99-149` | Priority read outside tx, written inside → lost update only in the narrow 30s-StartToClose-overlap race (no cross-run overlap; `sim_rosters` unique constraint backstops double-apply). Claim-status writes idempotent (`status='pending'` filter). *(V2-resolved: low severity.)* | Read priorities inside tx with `SELECT … FOR UPDATE` variant. | M | sonnet |
| B5 | `worker/shared/workflow_helpers.go:22` | Deprecated `WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING` (SA1019); orphan-cleanup semantics moved to `WorkflowIDConflictPolicy`. | Use `WorkflowIDConflictPolicy_TERMINATE_EXISTING`. | S | opus |
| B6 | `graph/simulation_helpers.go:135-149` | `GetSimStandingsLatestDate` real DB error swallowed (`if err == nil && …`) → silent empty standings. | Branch on `errors.Is(pgx.ErrNoRows)`; return other errors. | S | opus |
| B7 | `graph/simulation_helpers.go:637-688` | `createSimPoolImpl` inserts pool + N agents + starts workflow with no transaction → orphaned partial pool on failure. | Wrap inserts in pgx tx; start workflow after commit. | M | opus |
| B8 | `graph/resolver.go:326,332-340` | `getWorkflowResult` maps unknown Temporal status to zero enum silently; `FailureReason` only set on `Failed`, not TIMED_OUT/TERMINATED. | comma-ok map lookup; populate reason for all terminal states. | S | opus |
| B9 | `graph/data.resolvers.go` (all list resolvers) + `:118` | Unbounded queries; `searchPlayers` wraps raw input in `%…%` with no cap → OOM risk on `standings_snapshots` (~305k) / `searchPlayers("%")`. | Default+max LIMIT per list query. | M | opus |
| B10 | `cmd/worker.go:106,164-172` | Pool opened with `context.Background()`, `w.Run` uses Temporal InterruptCh — `cmd.Context()` signal wiring is dead; comment claims otherwise. | Pass `cmd.Context()`; drive drain off it or fix comment. | M | opus |
| B11 | `cmd/root.go:47` | `log.Fatal` inside `BindFlags` VisitAll (runs for every command's PreRunE) bypasses all `defer` cleanup on flag error. | Return error, propagate through `commonInit`. | M | sonnet |
| B12 | `cmd/sync.go:134-138` | Signal goroutine can't force-quit during blocking cancel RPC; leaks if `runSync` returns first. | `select` on `ctx.Done()`; reset OS signal after first receipt. | M | opus |
| B13 | `httpx/client.go:131-145` | `DownloadYahoo` discards matched `tokenErr` context, rebuilds a fresh error. | Reuse `tokenErr`, set `PublicURL` only if empty. | S | sonnet |
| B14 | `worker/player/verify_unmatched.go:117-119` | Blocking `time.Sleep` in activity loop, no ctx check / heartbeat → delayed shutdown, heartbeat-timeout retries. | `select{ctx.Done()/time.After}` + `RecordHeartbeat`. | S | sonnet |
| B15 | `metrics/metrics.go:127` + `telemetry_activity.go:48` | `puckdb_sim_day_duration_seconds` labeled by raw `pool_id` → unbounded Prometheus cardinality. | Drop `pool_id` label; per-pool detail via DB/trace. | S | sonnet |
| B16 | `worker/simulation/telemetry.go:224,263-267,353-356` | Cost/token accounting can diverge: `header.CostUsd` vs `agg`; parent `res.Usage` vs child `captures` sums; unguarded `int32` narrowing of token counts. | Derive cost+tokens from one source (`captures`); clamp/ widen to `int64`. | M | opus |
| B17 | `maurice/service.go:97-211` | Per-message `CreateMessage` in loop, no tx → crash leaves user msg + partial tool results, no answer (poisons `loadHistory`). | Wrap a chat turn's writes in a tx / persist post-loop atomically. | M | opus |

---

## Tier 3 — Idiomatic & dedup cleanup (sonnet) — high value, low risk

### T3-A · graph/ dedup pass — removes ~500-650 LOC (~20% of hand-written graph)
- `resolver.go:67-311` — ~30 near-identical `fetchX/cancelFetchX/fetchXResult/fetchXProgress`
  quartets → table-driven `map[string]workflowSpec` dispatch.
- `schema.resolvers.go:47-300` — collapse one-line pass-throughs to `Resolver` privates.
- `convert.go:215-291` — three byte-identical `convertGame*` mappers → one.
- `data.resolvers.go` — extract the copy-pasted `Queries == nil` guard into `r.db()`.
- Dedup helper pairs: `ptrStringIfNotEmpty`/`stringPtrIfNotEmpty` (`resolver.go:582` /
  `simulation_helpers.go:608`); `numericToFloat` duplicated in `simulation_helpers.go:355`
  despite the file already importing `simulation`.
- Effort L. Model sonnet. *(Single largest cleanup; do as one focused agent task.)*

### T3-B · simulation N+1 batching — `GetPlayersByIDs`
- Add `GetPlayersByIDs(ids []int64)` sqlc query (`WHERE id = ANY($1)`) and replace per-ID
  `GetPlayer` loops in: `state_activity.go:284-406` (600-candidate draft), `context_activity.go`,
  `simulation_helpers.go:267-281` (`assemblePlayerNameMap`), and the `SimPools` read path.
- `graph/simulation.resolvers.go:59-73` — `SimPools` is N+1³; add scalars-only list path +
  `ListSimRosterByPool` keyed by agent. Effort M–L. Model sonnet (opus for the SimPools
  resolver-shape decision if `@goField(forceResolver)` is introduced).

### T3-C · cmd/ structural dedup
- `worker.go:39-58` vs `176-195` (and every command) — flag-group list duplicated between
  `InitFlags`/`BindFlags`; define once per command, pass to both.
- `worker.go:232-407` — split 175-line `registerTasksActivities` into per-domain helpers.
- `graphql_status.go:12-124` — 11 near-identical `Get*Status` → one
  `workflowStatus(name, withYahoo)`.
- Queue-string literals `"tasks"`/`"admin"` (`worker.go:68,88,135`, has 4 TODO markers) →
  constants. Effort M. Model sonnet.

### T3-D · simulation LLM layer — schemas, prompts, params
- `tools.go:77-204` — 8 hand-written JSON schema string constants: add a
  `TestSchemasAreValidJSON` table test; extract the duplicated `reason` property fragment
  (`maxLength:200` ×5); set `Cacheable=true` on the last tool programmatically
  (`tools[len-1]`) so the cache boundary can't drift.
- `prompts.go:28-63` — roster composition / 9 categories are magic literals duplicated from
  `PoolConfig`; either derive from config or assert V1 fixed-roster + document. *(opus if
  wired to config — prompt/config divergence is correctness-adjacent.)*
- `telemetry.go:214-223` — 8-positional-param `RecordTurnTelemetry` (two swappable slices)
  → `TurnTelemetry` struct param.
- `types.go`/`prompts.go` — `any`-typed `Value`/`Last7`/`Season` stat fields push type
  discipline to runtime → generic `RosterRow[T Stats]` or concrete skater/goalie rows.
  Effort M–L. Model sonnet.

### T3-E · idiom sweep (batch of small sonnet fixes)
- Swallowed `numericToFloat` errors: `context_activity.go:337-345,402-408`.
- Duplicated lenient/strict parse switches: `recovery.go:333-380` → parameterize unmarshal fn.
- Bare position strings `{"C","LW","RW","D","G"}` repeated across `draft.go`/`validate.go`
  → use existing typed `Slot*` constants (a `"WR"` typo compiles today).
- Mixed sort idioms: `scoring.go` uses `sort.Slice`, `draft.go` uses `slices.SortFunc` →
  standardize on `slices.SortStableFunc`.
- `errors.New` vs `fmt.Errorf` no-verb: `draft_activity.go:388`.
- `cap`/`a` shadowing: `state_activity.go:24,300-314`.
- sqlc positional param names (`Column2/3`): name params in `context_activity.go` waiver query.
- `http.MethodGet` vs `"GET"`: `httpx/client.go:64`; ignored `w.Write` err: `httpx/yahoo.go:61`.
- `errors.Is` vs `==`: `cmd/redis_lock.go:21`.
- Config N+1 aliasing `append(Forwards, Defensemen...)`: `worker/nhl/import_edge.go:69,549`.
- Misleading `Insert*` names that are upserts: `worker/nhl/import_edge.go` → rename queries
  to `Upsert*` (matters for retry-safety auditing).
- Effort S each. Model sonnet. *(Bundle into 1-2 agent passes by package.)*

### T3-F · maurice → agentloop migration
- `maurice/service.go:124` duplicates ~120 lines of the tool loop that `llm/agentloop`
  already implements and tests (the agentloop doc even says Maurice should migrate).
  Migrate `Chat` onto `agentloop.Run` with a `ToolExecutor` closure. Effort L. Model opus
  (behavior-preserving migration of a stateful loop; also resolves the empty-content
  sentinel bug at `service.go:216`).

---

## Tier 4 — Repo hygiene (sonnet)

- **gofmt drift: 35 non-generated files** fail `gofmt -l` (missing spaces after commas in
  `worker/yahoo/import_activities.go`, `worker/player/activities.go`, misaligned const/tag
  blocks in `llm/anthropic.go`, `store/xml_yahoo.go`, etc.). **Fix:** `gofmt -w` the tree +
  add a CI gate (`gofmt -l` must be empty). Effort S.
- **staticcheck/golangci-lint gate** — several findings are lint-catchable (dead
  `totalPicks` `workflow.go:491`, SA1019 deprecated enum B5, S1016 struct conversion
  `boxscore.go:134`, builtin shadowing). Add `golangci-lint` to CI to catch the class.
  Effort S–M.
- **Performance polish (sonnet, opportunistic):** `httpx/client.go:150` builds a fresh
  `http.Client` per `DownloadPublic` call (no keep-alive); `store/storage_instrumented.go`
  runs ~19 regexes per FS op for a metric label (most are fixed substrings → `strings.Contains`);
  O(agents²) `agentID→config` linear scans in `workflow.go`/`manage_activity.go` → build a map once.

---

## Suggested execution order

1. **Tier 0 verify (V1, V2)** — 1 opus agent, ~30 min. Unblocks T1-B and B4.
2. **Tier 1** — the correctness core. Suggest grouping:
   - opus agent #1: T1-A (Selector determinism) + B5 (Temporal enum) — shared worker infra.
   - opus agent #2: T1-B/C/D/E — simulation workflow/activity retry+idempotency (one package, shared context).
   - opus agent #3: T1-F (lineup displacement) + T3-E validate/scoring idioms — `validate.go` cohesion.
   - opus agent #4: T1-G (cache trust boundaries) + B13 (httpx) — one package cluster.
   - opus agent #5: T1-H + B17 (maurice concurrency + tx).
   - Each fix lands with tests; run `puckdb-test` skill after each package.
3. **Tier 2 remainder** — mix of opus (B1,B2,B3,B6,B7,B8,B9,B12,B16) and sonnet (B10-alt,
   B11,B14,B15). Fan out by package.
4. **Tier 3 dedup** — sonnet agents, one per lettered package (T3-A graph is the big win).
   Behavior-preserving; verify with existing tests + `go build ./...`.
5. **Tier 4 hygiene** — `gofmt -w` + CI gates as a final PR.

Gate everything on: full test suite green (project rule: 100% pass before commit), `go build`
across the workspace, and no new `gofmt -l` / lint output.

## Effort/model tally

- **opus tasks:** ~17 correctness items (Tier 1 + Tier 2 opus + T3-F migration + Tier 0).
- **sonnet tasks:** ~6 dedup packages + ~15 idiom fixes + hygiene. The bulk of LOC changed
  is sonnet dedup (esp. T3-A ~500-650 LOC); the bulk of *risk* is the ~17 opus items.
