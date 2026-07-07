# Plan: Agent-to-Agent Trades in the Pool Simulation

**Date:** 2026-07-06
**Status:** Proposed
**Scope:** `worker/simulation/`, `database/migrations/`, `sqlcdb/queries/sim.sql`

Trades are the first *multi-party* action in the sim. Today everything is
agent-vs-environment: each agent's daily turn runs in isolation against a
morning snapshot, and the only cross-agent coupling is implicit contention over
the free-agent pool (first-processed wins) and waiver priority. PLAN.md defers
trades explicitly (`worker/simulation/PLAN.md:95`, `:1356`). This plan adds
them.

---

## 1. Design decisions (up front)

### 1.1 Trade model: persistent proposals, resolved next day

A trade is a **proposal object persisted in Postgres**, not an in-workflow
conversation:

- During its normal daily turn, an agent may call a new `propose_trade` tool.
- The proposal is committed with the rest of the turn (status `pending`).
- The **next day**, before roster management, a new *trade resolution* step
  presents pending proposals to the responder agent, who accepts/rejects/counters
  via an LLM activity.
- Accepted trades apply immediately (atomic roster swap), before that day's
  `ManageRoster` turns, so all agents see post-trade rosters in their contexts.

Why not same-day negotiation (workflow shuttles proposal → response inside one
day)? It's feasible — the workflow is the deterministic coordinator and could
chain two LLM activities — but:

- Persisted proposals survive `ContinueAsNew` (the workflow rolls over every 30
  days, `ContinueAsNewDayThreshold`, `workflow.go:62`) with zero extra state
  threading through `SimPoolWorkflowInput`.
- It mirrors real fantasy leagues (offers sit for a day).
- It keeps the daily phase sequence simple and the idempotency story clean
  (one marker per resolution day).

A one-round **counter-offer** is supported: responder may counter once; the
counter goes back to the original proposer on the *following* day as a new
proposal with `counter_of` set. No further counters (proposals expire instead).

### 1.2 Visibility: agents must see rival rosters

Today an agent has **no view of opponents' rosters** — only their category
standings (`loadStandingsRows`, `context_activity.go:307`). You cannot propose
a trade for a player you can't see. So this plan adds an **opponent rosters
block** to the daily context. This is also an independent quality win: agents
can reason about scarcity ("all good goalies are rostered") even when not
trading.

### 1.3 Fairness guardrail: none in V1

No veto, no value-balance check. LLM agents fleece each other — that's part of
the fun and shows up in the audit log. A league-level config knob
(`tradesEnabled`) gates the whole feature. A future veto/review mechanism can
be layered on the same tables (`status='vetoed'`).

---

## 2. Schema (new migration `0000XX_sim_trades`)

Follows the Yahoo precedent (`yahoo_transactions` type='trade': one transaction,
N players each with source→destination) and the `sim_lineup_moves` parent/child
pattern:

```sql
CREATE TABLE sim_trades (
    id             SERIAL PRIMARY KEY,
    pool_id        INT  NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    proposer_id    INT  NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    responder_id   INT  NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    proposed_date  DATE NOT NULL,
    resolve_date   DATE,                 -- date the responder acted
    status         TEXT NOT NULL DEFAULT 'pending',
        -- pending | accepted | rejected | countered | expired | voided
    counter_of     INT REFERENCES sim_trades(id),
    message        TEXT NOT NULL DEFAULT '',   -- proposer's pitch to responder
    response       TEXT NOT NULL DEFAULT '',   -- responder's reasoning
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sim_trade_players (
    trade_id      INT    NOT NULL REFERENCES sim_trades(id) ON DELETE CASCADE,
    player_id     BIGINT NOT NULL REFERENCES players(id),
    from_agent_id INT    NOT NULL REFERENCES sim_agents(id),
    to_agent_id   INT    NOT NULL REFERENCES sim_agents(id),
    PRIMARY KEY (trade_id, player_id)
);

-- one open proposal per proposer→responder pair per pool
CREATE UNIQUE INDEX ux_sim_trades_pending_pair
    ON sim_trades(pool_id, proposer_id, responder_id)
    WHERE status = 'pending';

CREATE INDEX idx_sim_trades_pool_status ON sim_trades(pool_id, status);
```

Also:

- `sim_rosters.acquired_via` gains value `'trade'` (TEXT with default, no CHECK
  to alter — confirm at implementation time).
- `sim_transactions.type` gains values `'trade_proposed'`, `'trade_accepted'`,
  `'trade_rejected'`, `'trade_expired'`, `'trade_voided'` (bare TEXT, no
  migration needed for the column; audit rows are written per agent side so the
  existing single-agent row shape still works — the `sim_trades` row is the
  authoritative multi-party record, `sim_transactions` rows are per-agent
  audit pointers with `reasoning` filled in).
- `sim_pools` gains `trades_enabled BOOLEAN NOT NULL DEFAULT false` and
  `trade_offer_ttl_days INT NOT NULL DEFAULT 2` (proposal expires if not
  resolved within N days).

## 3. Tool surface changes (`worker/simulation/tools.go`)

New tool for the **daily** phase:

```
propose_trade
  responder_team    string   (team name or agent id — use agent_id int, names collide)
  give_player_ids   []int64  (players from MY roster)
  get_player_ids    []int64  (players from THEIR roster)
  message           string   (pitch shown to the other agent)
```

New tools for the **trade resolution** phase (a new tool list `TradeTools()`):

```
accept_trade   { trade_id, reason }
reject_trade   { trade_id, reason }
counter_trade  { trade_id, give_player_ids, get_player_ids, message }   -- V1.1, optional
pass           -- implicit: responding with no tool call = leave pending until expiry? No —
               -- resolution is forced: the responder MUST accept or reject (or counter).
```

**Prompt-cache constraint (important, costs real money):** the last tool in
each list carries the Anthropic `cache_control` marker (`makeTool(...,
cacheable=true)`, see `DailyTools()` comment at `tools.go:262-267`).
`propose_trade` must be inserted **before** `update_notes` in `DailyTools()`,
never appended after it, or every live pool's prompt-prefix cache is
invalidated.

Validation sentinels added to `validate.go`: `ErrNotOnResponderRoster`,
`ErrNotOnProposerRoster`, `ErrTradeSelfTarget`, `ErrPendingTradeExists`,
`ErrTradeTooLarge` (cap at 3-for-3 in V1 to bound prompt size),
`ErrUnbalancedRosterAfterTrade` (both sides must end within roster capacity —
equal-count trades sidestep this; unequal counts require the shorthanded side
to have bench room).

## 4. Context changes (`worker/simulation/context_activity.go`)

`BuildManageRosterContext` gains two blocks:

1. **Opponent rosters** — loop `ListSimRosterByAgent` over all rival agents
   (or add one `ListSimRostersByPool` query and group in Go — one query
   preferred). Rendered compactly: team name, then `pos player_name (slot)`
   lines. Include each rival's weakest/strongest categories from the standings
   rows already loaded (`loadStandingsRows`) so the LLM can spot
   complementary needs without extra queries.
2. **My trade activity** — my pending outgoing proposals (so the agent doesn't
   spam duplicates; the partial unique index enforces it anyway), plus
   yesterday's resolutions (accepted/rejected + responder's reason) so the
   agent learns from outcomes.

The trade **resolution** context is a new, smaller builder
(`BuildTradeResponseContext`): responder's roster, proposer's roster, the
proposal (players both ways + message), both agents' standings rows, and the
responder's notes.

## 5. Workflow changes (`worker/simulation/workflow.go`)

`processOneDay` (`workflow.go:801-887`) gains a step between waivers (step 1)
and the free-agent pool build (step 2):

```
1. ProcessWaivers
1.5 ResolveTrades            ← NEW
    a. ExpireTrades activity      (pending proposals past TTL → 'expired')
    b. ListPendingTrades activity (due for resolution = proposed before today)
    c. For each proposal, in deterministic order (trade id ASC):
         BuildTradeResponseContext → RespondTrade (LLM activity)
         → CommitTradeResolution activity
2. BuildFreeAgentPool         (now reflects post-trade rosters — unchanged code)
3. per-agent ManageRoster loop (contexts see post-trade rosters — unchanged code)
4. CollectDayStats
5. UpdateStandings
```

Ordering across multiple pending proposals is by `sim_trades.id` ascending —
deterministic, no map iteration, no `SideEffect` needed. Sequential resolution
means an earlier accepted trade can invalidate a later proposal touching the
same players; commit-time revalidation (§6) handles that by voiding.

`StopAfter` (`types.go:159-179`) is unaffected — trades live inside the season
phase. Cost-cap pause (`SignalPause`, checked at `checkPause`,
`workflow.go:139-143`) already gates day boundaries; the trade LLM activity
uses the same cost accounting as `ManageRoster` (increments pool LLM cost in
its commit tx), so the cap covers trade turns automatically.

Activity options: reuse `llmActivityOptions` (`workflow.go:978`, MaxAttempts=2,
WaitForCancellation=true) for `RespondTrade`, `defaultActivityOptions` for the
bookkeeping activities.

## 6. Commit semantics (the correctness core)

Two commit paths, both inside a single `Tx.InTx` (`transactor.go:53`):

**A. Proposal commit** — extends `commitDailyTurn` (`manage_activity.go:562`).
`makeDailyToolExecutor` (`manage_activity.go:393`) gains a `ProposeTradeArgs`
case that validates against the working state (§3 sentinels) and accumulates
the proposal; commit writes the `sim_trades` + `sim_trade_players` rows and a
`trade_proposed` audit row. The existing `daily_turn_done` idempotency marker
(`manage_activity.go:625`) already covers retries — no new marker needed for
proposals.

**B. Resolution commit** — new `CommitTradeResolution` activity. In one tx:

1. **Idempotency probe:** if `sim_trades.status != 'pending'`, return recorded
   outcome (Temporal retried a completed commit — no-op, no re-bill).
2. **Commit-time revalidation** (mandatory — model on `applyWaiverResolution`,
   `waivers_activity.go:277`): re-verify *both* agents still own every player
   on their side of the trade and both post-swap rosters are legal. Any
   violation → `status='voided'` + audit rows; never let the
   `UNIQUE (pool_id, player_id)` constraint on `sim_rosters`
   (`000013_simulation.up.sql:127`) abort the transaction.
3. On accept: **DELETE both sides' outgoing roster rows first, then INSERT
   incoming rows** (`acquired_via='trade'`, incoming players land on BN) —
   ordering matters for the unique index. Cancel any pending waiver claims
   whose `drop_player_id` was traded away (mirror the vanished-drop handling
   in waivers).
4. Write `sim_trades.status/resolve_date/response`, per-agent
   `sim_transactions` audit rows, and the responder-turn telemetry
   (`sim_agent_turns` etc. — same pattern as `runDailyAgent`).
5. Increment pool LLM cost.

Retry subtleties inherited from the existing code that this respects:

- Activities retry (LLM MaxAttempts=2); the status probe in step 1 is the
  equivalent of `daily_turn_done`.
- Later-turn agents seeing the trade is *intended* here (resolution runs before
  all ManageRoster turns), avoiding the mid-day visibility asymmetry that
  same-day trades would create.

## 7. Config & CLI

- Pool YAML (`poolsim*.yaml`): `tradesEnabled: true`, `tradeOfferTtlDays: 2`.
  Parsed wherever `waiverDays` lives; stored on `sim_pools`.
- `puckdb sim trades <pool-id>` — list trades with status, players both ways,
  and both agents' reasoning (great demo output).
- Prompt guidance: the daily system prompt (in `prompts.go`) gains a short
  trades section explaining the propose→next-day-resolve lifecycle and that
  proposals are binding if accepted.

## 8. Phases

| Phase | Deliverable | Notes |
|-------|-------------|-------|
| 1 | Migration + sqlc queries (`sim_trades`, `sim_trade_players`, pool columns) | `./puckdb db migrate` only, never hand-apply |
| 2 | Opponent-rosters context block | Independent value; ship first, observe agent behavior |
| 3 | `propose_trade` tool + validation + proposal commit in `commitDailyTurn` | Cache-position rule in §3 |
| 4 | Resolution step: `ExpireTrades`, `BuildTradeResponseContext`, `RespondTrade`, `CommitTradeResolution`; wire into `processOneDay` | The correctness-critical phase — implement with go-genius-level care (Temporal retries, atomic swap) |
| 5 | CLI `sim trades`, prompt guidance, YAML config | |
| 6 | Tests | Unit: validation sentinels, revalidation voiding, idempotent re-commit, unbalanced-roster rejection. Workflow: temporal testsuite day-loop test where agent A proposes on day N and B accepts on day N+1; retry-injection test proving no double swap |
| 7 (opt) | `counter_trade` (one round) | Only after observing V1 accept/reject rates |

## 9. Risks / open questions

- **Prompt growth:** opponent rosters add ~N×15 lines to every daily context.
  For big pools this inflates cost. Mitigation: compact rendering; possibly
  cap to positional summaries + tradeable surplus. Measure with the existing
  telemetry (`sim_agent_turns` token columns) before and after Phase 2.
- **Trade spam:** partial unique index limits to one open proposal per
  directed pair; TTL expires stale offers. If agents still over-propose, add a
  per-day proposal cap in validation.
- **Weak local models:** ollama-hosted agents may misuse trade tools
  (invalid player ids). Validation-feedback loops already handle this (errors
  are returned as tool results, agent gets `MaxDailyToolRounds=5` attempts).
- **Player identity in tool args:** ids, not names, everywhere (names collide
  and LLMs typo them); the context blocks must always print ids next to names.
