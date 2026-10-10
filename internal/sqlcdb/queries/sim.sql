-- =============================================================================
-- Hockey Pool Simulator queries
-- See worker/simulation/PLAN.md for the design and worker/simulation/CHECKLIST.md
-- for which Phase 1.3 bullet each block satisfies.
-- =============================================================================

-- =============================================================================
-- sim_pools
-- =============================================================================

-- name: InsertSimPool :one
INSERT INTO sim_pools (
    name, season, status,
    num_teams, waiver_days, draft_rounds, max_llm_cost_usd_per_pool, categories,
    roster_c, roster_lw, roster_rw, roster_d, roster_g, roster_util, roster_bn, roster_ir,
    stop_after, max_season_days, start_date, end_date
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
RETURNING id, name, season, status, sim_date,
          num_teams, waiver_days, draft_rounds, max_llm_cost_usd_per_pool, categories,
          roster_c, roster_lw, roster_rw, roster_d, roster_g, roster_util, roster_bn, roster_ir,
          total_llm_cost_usd, workflow_id, created_at, updated_at, record_full_messages,
          stop_after, max_season_days, start_date, end_date;

-- name: GetSimPool :one
SELECT id, name, season, status, sim_date,
       num_teams, waiver_days, draft_rounds, max_llm_cost_usd_per_pool, categories,
       roster_c, roster_lw, roster_rw, roster_d, roster_g, roster_util, roster_bn, roster_ir,
       total_llm_cost_usd, workflow_id, created_at, updated_at, record_full_messages,
       stop_after, max_season_days, start_date, end_date
FROM sim_pools
WHERE id = $1;

-- name: ListSimPools :many
SELECT id, name, season, status, sim_date,
       num_teams, waiver_days, draft_rounds, max_llm_cost_usd_per_pool, categories,
       roster_c, roster_lw, roster_rw, roster_d, roster_g, roster_util, roster_bn, roster_ir,
       total_llm_cost_usd, workflow_id, created_at, updated_at, record_full_messages,
       stop_after, max_season_days, start_date, end_date
FROM sim_pools
ORDER BY id DESC;

-- name: UpdateSimPoolStatus :exec
UPDATE sim_pools
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateSimPoolDate :exec
UPDATE sim_pools
SET sim_date = $2, updated_at = NOW()
WHERE id = $1;

-- LockSimPool serializes waiver resolution per pool. It locks the pool row
-- (guaranteed to exist, unlike sim_waiver_priority rows, which a pool only
-- gets at its first ProcessWaivers) FOR NO KEY UPDATE: a second resolution
-- transaction for the same pool waits here until the first commits, then reads
-- the claims and priorities that commit left behind. NO KEY UPDATE does not
-- block the KEY SHARE locks taken by foreign-key checks, so concurrent inserts
-- into child tables (claims, transactions) are not held up. Must run inside a
-- Transactor.InTx callback.
-- name: LockSimPool :one
SELECT id FROM sim_pools WHERE id = $1 FOR NO KEY UPDATE;

-- IncrementSimPoolLLMCost is the cost-cap accountant. Returns the new total
-- so the caller can check whether the cap fired in the same round-trip.
-- name: IncrementSimPoolLLMCost :one
UPDATE sim_pools
SET total_llm_cost_usd = total_llm_cost_usd + $2, updated_at = NOW()
WHERE id = $1
RETURNING total_llm_cost_usd;

-- =============================================================================
-- sim_agents
-- =============================================================================

-- name: InsertSimAgent :one
-- draft_position is intentionally absent — it's NULL until the
-- workflow runs RecordDraftOrder to persist the SideEffect-shuffled
-- order. Setting it at insert time would mean "input order" which is
-- not what draft_position represents.
--
-- team_name defaults to '' here; PickTeamName in Phase 0 of the
-- workflow UPDATEs it to the agent-chosen name. Until then, displays
-- fall back to "agent #<id>".
INSERT INTO sim_agents (
    pool_id, provider, model, strategy,
    timeout_seconds, temperature, api_base, max_tokens
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSimAgent :one
SELECT * FROM sim_agents WHERE id = $1;

-- name: ListSimAgentsByPool :many
-- NULLS LAST + id tiebreak so pre-shuffle (all NULL) sort is stable,
-- post-shuffle agents come out in actual draft order.
SELECT * FROM sim_agents
WHERE pool_id = $1
ORDER BY draft_position NULLS LAST, id;

-- name: SetSimAgentDraftPosition :exec
UPDATE sim_agents SET draft_position = $2 WHERE id = $1;

-- name: SetSimAgentTeamNameAndSummary :exec
-- Written by PickTeamName as a single update so the team_name and
-- the agent's self-summary land atomically — a half-written state
-- (name set but summary blank, or vice versa) would confuse the tail
-- and standings displays which expect both or neither.
UPDATE sim_agents SET team_name = $2, strategy_summary = $3 WHERE id = $1;

-- name: CountSimDraftPicks :one
SELECT COUNT(*) FROM sim_transactions
WHERE pool_id = $1 AND type = 'draft_pick';

-- The DB CHECK (octet_length(notes) <= 50000) gate-keeps oversized writes;
-- the activity surfaces violations to the agent as a tool-call error.
-- name: UpdateSimAgentNotes :exec
UPDATE sim_agents
SET notes = $2
WHERE id = $1;

-- =============================================================================
-- sim_rosters
-- =============================================================================

-- name: InsertSimRoster :exec
INSERT INTO sim_rosters (pool_id, agent_id, player_id, slot, acquired_at, acquired_via)
VALUES ($1, $2, $3, $4, $5, $6);

-- ExistsSimRosterPlayer is the commit-time claimability probe. A player on
-- ANY agent's roster in the pool fails the UNIQUE (pool_id, player_id)
-- constraint on InsertSimRoster, so add_player / waiver resolution re-check
-- this inside the commit tx (the morning FA snapshot / filing-time validation
-- can be stale by the time the write lands) and convert a would-be constraint
-- violation into a clean rejection.
-- name: ExistsSimRosterPlayer :one
SELECT EXISTS (
    SELECT 1 FROM sim_rosters
    WHERE pool_id = $1 AND player_id = $2
) AS exists;

-- DeleteSimRosterRows removes one player from an agent's roster and reports
-- the rows affected. Its only caller, removeAndLogDrop (explicit drops,
-- add_player replacements, waiver-claim drops), runs it after the player was
-- validated as rostered, so 0 rows affected is an invariant violation that
-- fails the transaction — a waiver drop player who left the roster since filing
-- is detected by the plan's SELECT, not by this count.
-- name: DeleteSimRosterRows :execrows
DELETE FROM sim_rosters
WHERE pool_id = $1 AND agent_id = $2 AND player_id = $3;

-- name: UpdateSimRosterSlot :exec
UPDATE sim_rosters
SET slot = $4
WHERE pool_id = $1 AND agent_id = $2 AND player_id = $3;

-- name: ListSimRosterByAgent :many
SELECT pool_id, agent_id, player_id, slot, acquired_at, acquired_via
FROM sim_rosters
WHERE pool_id = $1 AND agent_id = $2
ORDER BY slot, player_id;

-- ListSimActiveRosterByAgent returns only active-lineup slots (excludes BN/IR).
-- Used by CollectDayStatsActivity — only active players score for the agent.
-- name: ListSimActiveRosterByAgent :many
SELECT pool_id, agent_id, player_id, slot, acquired_at, acquired_via
FROM sim_rosters
WHERE pool_id = $1 AND agent_id = $2 AND slot NOT IN ('BN', 'IR')
ORDER BY slot, player_id;

-- name: ListSimRosterByPool :many
SELECT pool_id, agent_id, player_id, slot, acquired_at, acquired_via
FROM sim_rosters
WHERE pool_id = $1
ORDER BY agent_id, slot, player_id;

-- =============================================================================
-- sim_agent_daily_player_stats — per-player attribution layer (source of truth)
-- =============================================================================

-- Idempotent rerun: row set is deterministic across retries because the day's
-- roster locks before scoring runs (PLAN.md Day Loop step 4).
-- name: UpsertSimAgentDailyPlayerStat :exec
INSERT INTO sim_agent_daily_player_stats (
    pool_id, agent_id, date, player_id, category, value, goalie_ga, goalie_toi_seconds
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (pool_id, agent_id, date, player_id, category) DO UPDATE SET
    value = EXCLUDED.value,
    goalie_ga = EXCLUDED.goalie_ga,
    goalie_toi_seconds = EXCLUDED.goalie_toi_seconds;

-- ListSimAgentDailyPlayerStatsByPlayer is the diagnostics query: "which player
-- on agent X's roster scored those goals on date Y?"
-- name: ListSimAgentDailyPlayerStatsByPlayer :many
SELECT pool_id, agent_id, date, player_id, category, value, goalie_ga, goalie_toi_seconds
FROM sim_agent_daily_player_stats
WHERE pool_id = $1 AND agent_id = $2 AND player_id = $3
ORDER BY date, category;

-- =============================================================================
-- sim_agent_daily_stats — per-agent rollup of sim_agent_daily_player_stats
-- =============================================================================

-- AggregateSimAgentDailyStats rolls per-player rows up to per-agent for one day.
-- Counting categories: SUM(value); GAA: stores raw components only — the GAA
-- value field stays 0 here since per-day GAA isn't itself a score (totals query
-- computes GAA from accumulated components).
-- name: AggregateSimAgentDailyStats :exec
INSERT INTO sim_agent_daily_stats (
    pool_id, agent_id, date, category, value, goalie_ga, goalie_toi_seconds
)
SELECT
    p.pool_id, p.agent_id, p.date, p.category,
    SUM(p.value),
    NULLIF(SUM(COALESCE(p.goalie_ga, 0)), 0),
    NULLIF(SUM(COALESCE(p.goalie_toi_seconds, 0)), 0)
FROM sim_agent_daily_player_stats p
WHERE p.pool_id = $1 AND p.agent_id = $2 AND p.date = $3
GROUP BY p.pool_id, p.agent_id, p.date, p.category
ON CONFLICT (pool_id, agent_id, date, category) DO UPDATE SET
    value = EXCLUDED.value,
    goalie_ga = EXCLUDED.goalie_ga,
    goalie_toi_seconds = EXCLUDED.goalie_toi_seconds;

-- =============================================================================
-- sim_agent_totals — recomputed from sim_agent_daily_stats after each day
-- =============================================================================

-- RecomputeSimAgentTotalsCounting handles every category EXCEPT 'GAA'.
-- name: RecomputeSimAgentTotalsCounting :exec
INSERT INTO sim_agent_totals (pool_id, agent_id, category, value)
SELECT d.pool_id, d.agent_id, d.category, SUM(d.value)
FROM sim_agent_daily_stats d
WHERE d.pool_id = $1 AND d.category <> 'GAA'
GROUP BY d.pool_id, d.agent_id, d.category
ON CONFLICT (pool_id, agent_id, category) DO UPDATE SET
    value = EXCLUDED.value;

-- RecomputeSimAgentTotalsGAA computes GAA from raw components per PLAN.md:
-- season GAA = SUM(goalie_ga) / SUM(goalie_toi_seconds) * 3600.
--
-- goalie_ga is COALESCEd: the daily rollup stores NULL for a zero GA
-- sum (NULLIF pattern above), so a goalie whose only appearances are
-- SHUTOUTS yields SUM(goalie_ga) = NULL with positive TOI — without
-- the COALESCE the value column computes NULL / toi = NULL and the
-- insert violates sim_agent_totals.value NOT NULL, wedging
-- CollectDayStats in terminal retries.
-- name: RecomputeSimAgentTotalsGAA :exec
INSERT INTO sim_agent_totals (pool_id, agent_id, category, value, goalie_ga, goalie_toi_seconds)
SELECT
    d.pool_id, d.agent_id, 'GAA',
    CASE WHEN SUM(d.goalie_toi_seconds) > 0
         THEN (COALESCE(SUM(d.goalie_ga), 0)::NUMERIC / SUM(d.goalie_toi_seconds)) * 3600
         ELSE 0 END,
    COALESCE(SUM(d.goalie_ga), 0),
    SUM(d.goalie_toi_seconds)
FROM sim_agent_daily_stats d
WHERE d.pool_id = $1 AND d.category = 'GAA'
GROUP BY d.pool_id, d.agent_id
ON CONFLICT (pool_id, agent_id, category) DO UPDATE SET
    value = EXCLUDED.value,
    goalie_ga = EXCLUDED.goalie_ga,
    goalie_toi_seconds = EXCLUDED.goalie_toi_seconds;

-- name: ListSimAgentTotalsByPool :many
SELECT pool_id, agent_id, category, value, goalie_ga, goalie_toi_seconds
FROM sim_agent_totals
WHERE pool_id = $1
ORDER BY agent_id, category;

-- =============================================================================
-- sim_standings
-- =============================================================================

-- name: UpsertSimStanding :exec
INSERT INTO sim_standings (pool_id, date, agent_id, category, value, roto_points)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (pool_id, date, agent_id, category) DO UPDATE SET
    value = EXCLUDED.value,
    roto_points = EXCLUDED.roto_points;

-- name: ListSimStandingsByDate :many
SELECT pool_id, date, agent_id, category, value, roto_points
FROM sim_standings
WHERE pool_id = $1 AND date = $2
ORDER BY agent_id, category;

-- GetSimStandingsLatestDate returns the most recent date that has standings rows
-- for the pool. Callers join with ListSimStandingsByDate to fetch the snapshot.
-- name: GetSimStandingsLatestDate :one
SELECT MAX(date)::DATE AS latest_date
FROM sim_standings
WHERE pool_id = $1;

-- ListSimStandingsByPool is the historical lookup behind simStandingsHistory.
-- name: ListSimStandingsByPool :many
SELECT pool_id, date, agent_id, category, value, roto_points
FROM sim_standings
WHERE pool_id = $1
ORDER BY date, agent_id, category;

-- =============================================================================
-- sim_transactions — full audit log
-- =============================================================================

-- Per-type insert helpers populate exactly the columns named in PLAN.md
-- "Per-type column population". Helpers RETURN the row so the caller can chain
-- sim_lineup_moves inserts using the new transaction id (lineup_set only).

-- name: InsertSimTransactionDraftPick :one
INSERT INTO sim_transactions (
    pool_id, agent_id, date, type, player_id, reasoning, round, pick
)
VALUES ($1, $2, $3, 'draft_pick', $4, $5, $6, $7)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionAdd :one
INSERT INTO sim_transactions (
    pool_id, agent_id, date, type, player_id, reasoning, drop_player_id
)
VALUES ($1, $2, $3, 'add', $4, $5, $6)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionClaim :one
INSERT INTO sim_transactions (
    pool_id, agent_id, date, type, player_id, reasoning, drop_player_id
)
VALUES ($1, $2, $3, 'claim', $4, $5, $6)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionDrop :one
INSERT INTO sim_transactions (
    pool_id, agent_id, date, type, player_id, reasoning
)
VALUES ($1, $2, $3, 'drop', $4, $5)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionLineupSet :one
INSERT INTO sim_transactions (pool_id, agent_id, date, type, reasoning)
VALUES ($1, $2, $3, 'lineup_set', $4)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionPass :one
INSERT INTO sim_transactions (pool_id, agent_id, date, type, reasoning)
VALUES ($1, $2, $3, 'pass', $4)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionError :one
INSERT INTO sim_transactions (
    pool_id, agent_id, date, type, reasoning, error_kind, error_detail
)
VALUES ($1, $2, $3, 'error', $4, $5, $6)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- name: InsertSimTransactionCostCapReached :one
INSERT INTO sim_transactions (
    pool_id, agent_id, date, type, cost_usd, cap_usd
)
VALUES ($1, $2, $3, 'cost_cap_reached', $4, $5)
RETURNING id, pool_id, agent_id, date, type, player_id, reasoning,
          round, pick, drop_player_id, error_kind, error_detail,
          cost_usd, cap_usd, created_at;

-- InsertSimTransactionDailyTurnDone writes the dedicated end-of-turn marker
-- that ExistsSimDailyTurnMarker probes. Written once per daily turn inside
-- commitDailyTurn's tx, regardless of whether the turn produced actions, a
-- pass, or an error — decoupling the idempotency signal from the audit-log
-- rows so an unrelated row (draft_pick, waiver add/drop, cost_cap_reached)
-- at the same (pool, agent, date) can't spuriously skip the turn.
-- name: InsertSimTransactionDailyTurnDone :exec
INSERT INTO sim_transactions (pool_id, agent_id, date, type)
VALUES ($1, $2, $3, 'daily_turn_done');

-- ListSimTransactions is the paginated audit log (powers the `log` workflow
-- query and the simTransactions GraphQL query). agent_id is optional: pass 0
-- to omit the agent filter.
-- name: ListSimTransactions :many
SELECT id, pool_id, agent_id, date, type, player_id, reasoning,
       round, pick, drop_player_id, error_kind, error_detail,
       cost_usd, cap_usd, created_at
FROM sim_transactions
WHERE pool_id = $1
  AND ($2::INT = 0 OR agent_id = $2)
  AND ($3::DATE IS NULL OR date = $3)
ORDER BY id DESC
LIMIT $4;

-- GetSimTransactionDraftPick is the idempotency probe for DraftPickActivity.
-- Round + pick uniquely identify a draft slot; if a row exists we skip the LLM.
-- name: GetSimTransactionDraftPick :one
SELECT id, pool_id, agent_id, date, type, player_id, reasoning,
       round, pick, drop_player_id, error_kind, error_detail,
       cost_usd, cap_usd, created_at
FROM sim_transactions
WHERE pool_id = $1 AND agent_id = $2 AND type = 'draft_pick'
  AND round = $3 AND pick = $4;

-- ExistsSimDailyTurnMarker is the idempotency probe for ManageRosterActivity.
-- It matches ONLY the dedicated 'daily_turn_done' marker row, not any
-- transaction at (pool, agent, date). Filtering by type prevents day-1
-- draft_pick rows (stamped at SeasonStartDate), same-day waiver add/drop
-- rows, and cost_cap_reached audit rows from spuriously tripping the probe
-- and skipping a legitimate daily turn.
-- name: ExistsSimDailyTurnMarker :one
SELECT EXISTS (
    SELECT 1 FROM sim_transactions
    WHERE pool_id = $1 AND agent_id = $2 AND date = $3
      AND type = 'daily_turn_done'
) AS exists;

-- =============================================================================
-- sim_lineup_moves — child rows of lineup_set transactions
-- =============================================================================

-- name: InsertSimLineupMove :exec
INSERT INTO sim_lineup_moves (
    transaction_id, sequence, player_id, from_slot, to_slot, displaced_player_id
)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListSimLineupMovesByTransaction :many
SELECT transaction_id, sequence, player_id, from_slot, to_slot, displaced_player_id
FROM sim_lineup_moves
WHERE transaction_id = $1
ORDER BY sequence;

-- =============================================================================
-- Free-agent pool — runs once per (pool_id, sim_date), shared across agents
-- See PLAN.md > "Free Agent Pool" > "Query shape" for the bounded-scan
-- performance characterization.
-- =============================================================================

-- name: ListSimFreeAgentCandidates :many
WITH active_players AS (
    SELECT DISTINCT gss.player_id
    FROM game_skater_stats gss
    JOIN games g ON gss.game_id = g.id
    WHERE g.season = $1 AND g.game_type = 'regular_season' AND g.game_date <= $2
    UNION
    SELECT DISTINCT ggs.player_id
    FROM game_goalie_stats ggs
    JOIN games g ON ggs.game_id = g.id
    WHERE g.season = $1 AND g.game_type = 'regular_season' AND g.game_date <= $2
)
SELECT player_id FROM active_players
EXCEPT SELECT r.player_id FROM sim_rosters r WHERE r.pool_id = $3
EXCEPT SELECT wc.player_id FROM sim_waiver_claims wc
       WHERE wc.pool_id = $3 AND wc.status = 'pending'
EXCEPT SELECT t.player_id FROM sim_transactions t
       WHERE t.pool_id = $3 AND t.type = 'drop'
         AND t.date > ($2::DATE - ($4::INT) * INTERVAL '1 day');

-- =============================================================================
-- sim_waiver_priority
-- =============================================================================

-- name: InsertSimWaiverPriority :exec
INSERT INTO sim_waiver_priority (pool_id, agent_id, priority)
VALUES ($1, $2, $3);

-- InitSimWaiverPriority gives every agent of a pool one priority row in reverse
-- draft order (the last round-1 pick gets priority 1, PLAN.md > "Waivers"). It
-- inserts nothing when the pool already has priority rows (a retry, or a pool
-- whose order has since rotated) or when any agent has no recorded
-- draft_position yet, so it never duplicates, reorders or half-initializes.
-- The caller holds LockSimPool, which makes the NOT EXISTS check race-free,
-- and verifies completeness afterwards.
-- name: InitSimWaiverPriority :execrows
INSERT INTO sim_waiver_priority (pool_id, agent_id, priority)
SELECT a.pool_id, a.id, ROW_NUMBER() OVER (ORDER BY a.draft_position DESC, a.id)
FROM sim_agents a
WHERE a.pool_id = $1
  AND NOT EXISTS (SELECT 1 FROM sim_waiver_priority p WHERE p.pool_id = $1)
  AND NOT EXISTS (
      SELECT 1 FROM sim_agents u WHERE u.pool_id = $1 AND u.draft_position IS NULL
  );

-- ListSimWaiverPriorityByPool locks the returned rows FOR UPDATE. Its only
-- caller (ProcessWaivers) reads the priority order and later writes a
-- restamped order back to the same rows within the same transaction. The
-- per-pool serialization itself comes from LockSimPool, taken first: these row
-- locks alone protect nothing when the pool has no rows yet. Must be called
-- from inside a Transactor.InTx callback; taking a row lock outside a
-- transaction has no effect beyond the statement itself.
-- name: ListSimWaiverPriorityByPool :many
SELECT pool_id, agent_id, priority
FROM sim_waiver_priority
WHERE pool_id = $1
ORDER BY priority
FOR UPDATE;

-- UpdateSimWaiverPriority lets the caller re-rank an agent (e.g., winner drops
-- to bottom). The whole table is small (≤ num_teams rows per pool) so we just
-- restamp priorities from the application layer.
-- name: UpdateSimWaiverPriority :exec
UPDATE sim_waiver_priority
SET priority = $3
WHERE pool_id = $1 AND agent_id = $2;

-- =============================================================================
-- sim_waiver_claims
-- =============================================================================

-- name: InsertSimWaiverClaim :one
INSERT INTO sim_waiver_claims (
    pool_id, agent_id, player_id, drop_player_id, filed_date, process_date
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, pool_id, agent_id, player_id, drop_player_id,
          filed_date, process_date, status, resolved_at, created_at;

-- name: GetSimWaiverClaim :one
SELECT id, pool_id, agent_id, player_id, drop_player_id,
       filed_date, process_date, status, resolved_at, created_at
FROM sim_waiver_claims
WHERE id = $1;

-- ListSimWaiverClaimsDue returns pending claims ready to process today, ordered
-- by player so ProcessWaiversActivity can group contested players in one pass.
-- name: ListSimWaiverClaimsDue :many
SELECT id, pool_id, agent_id, player_id, drop_player_id,
       filed_date, process_date, status, resolved_at, created_at
FROM sim_waiver_claims
WHERE pool_id = $1 AND status = 'pending' AND process_date <= $2
ORDER BY player_id, agent_id;

-- ListSimWaiverClaimsForDuePlayers returns EVERY pending claim on any player
-- that has at least one claim due today (process_date <= $2). Cross-day claims
-- on the same player are processed together so waiver priority is honored: if a
-- low-priority agent filed first (earlier process_date) but a high-priority
-- agent also has a pending claim, both land in the same contested group and the
-- high-priority agent wins. Grouping only by "due today" (ListSimWaiverClaimsDue)
-- would let the earlier filer win uncontested, bypassing priority.
-- Ordered by player so ProcessWaiversActivity can group in one pass. The rows
-- are locked FOR UPDATE: ProcessWaivers reads them inside its transaction,
-- after LockSimPool, so it resolves the claims as they are now and not a
-- snapshot taken before another attempt committed.
-- name: ListSimWaiverClaimsForDuePlayers :many
SELECT c.id, c.pool_id, c.agent_id, c.player_id, c.drop_player_id,
       c.filed_date, c.process_date, c.status, c.resolved_at, c.created_at
FROM sim_waiver_claims c
WHERE c.pool_id = $1 AND c.status = 'pending'
  AND c.player_id IN (
      SELECT d.player_id FROM sim_waiver_claims d
      WHERE d.pool_id = $1 AND d.status = 'pending' AND d.process_date <= $2
  )
ORDER BY c.player_id, c.agent_id
FOR UPDATE;

-- ListSimWaiverClaimsPending exposes the live claim queue for context-builder.
-- name: ListSimWaiverClaimsPending :many
SELECT id, pool_id, agent_id, player_id, drop_player_id,
       filed_date, process_date, status, resolved_at, created_at
FROM sim_waiver_claims
WHERE pool_id = $1 AND status = 'pending'
ORDER BY process_date, player_id, agent_id;

-- ListSimWaiverClaimsPendingByAgent returns one agent's pending claims —
-- feeds the daily working state so ValidateClaimPlayer can reject a duplicate
-- pending claim on a player the agent already has a claim on (which would
-- otherwise violate ux_sim_waiver_claims_pending_one_per_agent_player and roll
-- back the whole daily turn).
-- name: ListSimWaiverClaimsPendingByAgent :many
SELECT id, pool_id, agent_id, player_id, drop_player_id,
       filed_date, process_date, status, resolved_at, created_at
FROM sim_waiver_claims
WHERE pool_id = $1 AND agent_id = $2 AND status = 'pending'
ORDER BY player_id;

-- CancelSimWaiverClaimsForPlayer marks every still-pending claim on a player as
-- cancelled EXCEPT the one named in $4 (the winning claim, already flipped to
-- 'won'). Called in the resolution tx after a win so a later-dated pending claim
-- on the same player can't resolve as a phantom uncontested win and hit the
-- UNIQUE (pool_id, player_id) roster constraint.
-- name: CancelSimWaiverClaimsForPlayer :exec
UPDATE sim_waiver_claims
SET status = 'cancelled', resolved_at = $3
WHERE pool_id = $1 AND player_id = $2 AND status = 'pending' AND id <> $4;

-- ResolveSimWaiverClaim moves a pending claim to a terminal status. Only a
-- pending claim changes: a claim already won, lost or cancelled is left alone
-- and the statement reports 0 rows, which the caller treats as a failed
-- transaction (a resolution working from stale claims must not rewrite a
-- won claim as lost).
-- name: ResolveSimWaiverClaim :execrows
UPDATE sim_waiver_claims
SET status = $2, resolved_at = $3
WHERE id = $1 AND status = 'pending';

-- ListSimPlayersOnWaivers lists players visible to claim_player: dropped within
-- the last waiver_days, with no winning claim recorded yet. Returns the most
-- recent drop transaction's date so the caller can compute "clears on" UI.
-- name: ListSimPlayersOnWaivers :many
SELECT DISTINCT ON (t.player_id)
    t.player_id,
    t.date AS dropped_on,
    (t.date + (sqlc.arg('waiver_days')::INT) * INTERVAL '1 day')::DATE AS clears_on
FROM sim_transactions t
WHERE t.pool_id = sqlc.arg('pool_id')
  AND t.type = 'drop'
  AND t.date > (sqlc.arg('sim_date')::DATE - (sqlc.arg('waiver_days')::INT) * INTERVAL '1 day')
  AND NOT EXISTS (
      SELECT 1 FROM sim_waiver_claims wc
      WHERE wc.pool_id = t.pool_id
        AND wc.player_id = t.player_id
        AND wc.status = 'won'
        AND wc.resolved_at >= t.date
  )
ORDER BY t.player_id, t.date DESC;

-- =============================================================================
-- Player-team and schedule lookups (read-only against shared NHL tables)
-- =============================================================================

-- GetPlayerCurrentTeam derives the player's team from their most recent
-- (skater or goalie) game stats row up to sim_date — see PLAN.md "Player Team
-- Assignment" for the rationale and the V1 limitation around day-1/trade-gap/
-- pre-debut boundary cases (returns NULL for affected players).
-- name: GetPlayerCurrentTeam :one
SELECT team_id, last_game_date FROM (
    SELECT gss.team_id, g.game_date AS last_game_date
    FROM game_skater_stats gss
    JOIN games g ON gss.game_id = g.id
    WHERE gss.player_id = $1 AND g.season = $2 AND g.game_type = 'regular_season'
      AND g.game_date <= $3
    UNION ALL
    SELECT ggs.team_id, g.game_date AS last_game_date
    FROM game_goalie_stats ggs
    JOIN games g ON ggs.game_id = g.id
    WHERE ggs.player_id = $1 AND g.season = $2 AND g.game_type = 'regular_season'
      AND g.game_date <= $3
) recent
ORDER BY last_game_date DESC
LIMIT 1;

-- ListSimDayGames returns completed regular-season games of the season on
-- the date — drives the "do we score today?" branch in the day loop. The
-- season predicate keeps another season's games on the same calendar date
-- out of pool scoring. Completed games are stored as 'OFF' (historical) or
-- 'FINAL'; match both, as game.sql does.
-- name: ListSimDayGames :many
SELECT id, season, game_type, game_date, game_state,
       home_team_id, away_team_id, home_team_score, away_team_score
FROM games
WHERE season = $1
  AND game_date = $2
  AND game_type = 'regular_season'
  AND game_state IN ('OFF', 'FINAL')
ORDER BY id;

-- CountSimGamesNext7Days counts regular-season games between sim_date and
-- sim_date+7 (exclusive of sim_date itself per PLAN.md "games_next_7_days":
-- the count is forward-looking from the next calendar day). Counts FUT/LIVE
-- and FINAL alike — replays are deterministic against historical data, so
-- "scheduled" and "played" both indicate a game on the team's calendar.
-- name: CountSimGamesNext7Days :one
SELECT COUNT(*)
FROM games
WHERE season = $1
  AND game_type = 'regular_season'
  AND game_date > $2
  AND game_date <= ($2::DATE + INTERVAL '7 days')::DATE
  AND (home_team_id = $3 OR away_team_id = $3);

-- GetTeamGoalsPerGame returns the team's GF and GA averages over completed
-- regular-season games up to sim_date — used in the daily context payload to
-- give agents a coarse strength signal for opponent matchups.
-- name: GetTeamGoalsPerGame :one
SELECT
    COUNT(*)::INT AS games_played,
    COALESCE(AVG(
        CASE WHEN home_team_id = $3 THEN home_team_score
             ELSE away_team_score END
    ), 0)::NUMERIC AS goals_for_per_game,
    COALESCE(AVG(
        CASE WHEN home_team_id = $3 THEN away_team_score
             ELSE home_team_score END
    ), 0)::NUMERIC AS goals_against_per_game
FROM games
WHERE season = $1
  AND game_type = 'regular_season'
  AND game_state IN ('OFF', 'FINAL')
  AND game_date <= $2
  AND (home_team_id = $3 OR away_team_id = $3);

-- =============================================================================
-- Turn telemetry — every LLM-driven turn (team_name, draft, daily) records
-- header + per-round usage + per-tool-call + (optionally) the full message
-- list. See database/migrations/000018_simulation_turn_telemetry.up.sql for
-- the schema rationale.
-- =============================================================================

-- DeleteSimAgentTurnIdempotent removes any prior turn for the same
-- (pool, agent, phase, sim_date, pick_number) coordinate so a re-run of
-- the activity (Temporal retry, manual re-invocation) can rewrite the row
-- instead of colliding with the partial unique indexes. sim_date is
-- matched with IS NOT DISTINCT FROM so the team_name phase (sim_date NULL)
-- and the draft/daily phases (sim_date set) share one query. pick_number
-- is part of the key so each draft pick is its own idempotency unit —
-- without it, pick N would delete pick N-1's row (all of an agent's picks
-- share phase='draft' + sim_date=season start). Non-draft phases use
-- pick_number 0. ON DELETE CASCADE cleans up children (rounds,
-- tool_calls, messages).
-- name: DeleteSimAgentTurnIdempotent :exec
DELETE FROM sim_agent_turns
 WHERE pool_id     = $1
   AND agent_id    = $2
   AND phase       = $3
   AND sim_date IS NOT DISTINCT FROM $4
   AND pick_number = $5;

-- InsertSimAgentTurn writes the turn header and returns the new id. The
-- caller chains the per-round / per-tool-call / per-message batch inserts
-- using this id within the same atomic-commit block.
-- name: InsertSimAgentTurn :one
INSERT INTO sim_agent_turns (
    pool_id, agent_id, sim_date, phase, pick_number,
    status, skip_reason, error_kind, error_detail,
    provider, model, temperature, max_tokens,
    rounds, prompt_tokens, completion_tokens,
    cache_creation_tokens, cache_read_tokens,
    cost_usd, latency_ms, final_text,
    started_at, completed_at
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16,
    $17, $18,
    $19, $20, $21,
    $22, $23
)
RETURNING id;

-- ListSimAgentTurns is the timeline view for one pool. Pass agent_id = 0
-- to skip the agent filter; pass phase = '' to skip the phase filter.
-- name: ListSimAgentTurns :many
SELECT id, pool_id, agent_id, sim_date, phase, pick_number,
       status, skip_reason, error_kind, error_detail,
       provider, model, temperature, max_tokens,
       rounds, prompt_tokens, completion_tokens,
       cache_creation_tokens, cache_read_tokens,
       cost_usd, latency_ms, final_text,
       started_at, completed_at
FROM sim_agent_turns
WHERE pool_id = $1
  AND ($2::INT = 0 OR agent_id = $2)
  AND ($3::TEXT = '' OR phase = $3)
ORDER BY started_at DESC, id DESC
LIMIT $4;

-- GetSimAgentTurn returns a single turn by id. Used by the GraphQL drill-
-- down resolver alongside the rounds / tool_calls / messages children.
-- name: GetSimAgentTurn :one
SELECT id, pool_id, agent_id, sim_date, phase, pick_number,
       status, skip_reason, error_kind, error_detail,
       provider, model, temperature, max_tokens,
       rounds, prompt_tokens, completion_tokens,
       cache_creation_tokens, cache_read_tokens,
       cost_usd, latency_ms, final_text,
       started_at, completed_at
FROM sim_agent_turns
WHERE id = $1;

-- InsertSimAgentTurnRound is a per-round usage row. Batched via
-- :copyfrom — one COPY for all rounds of one turn beats N separate INSERTs
-- on the typical 1-4 round turn shape.
-- name: InsertSimAgentTurnRound :copyfrom
INSERT INTO sim_agent_turn_rounds (
    turn_id, round_index, assistant_text,
    prompt_tokens, completion_tokens,
    cache_creation_tokens, cache_read_tokens,
    latency_ms
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- ListSimAgentTurnRounds returns all rounds of one turn, in order.
-- name: ListSimAgentTurnRounds :many
SELECT turn_id, round_index, assistant_text,
       prompt_tokens, completion_tokens,
       cache_creation_tokens, cache_read_tokens,
       latency_ms
FROM sim_agent_turn_rounds
WHERE turn_id = $1
ORDER BY round_index;

-- InsertSimAgentToolCall is one tool-call row. Batched via :copyfrom.
-- name: InsertSimAgentToolCall :copyfrom
INSERT INTO sim_agent_tool_calls (
    turn_id, round_index, sequence,
    tool_name, recovered_name,
    arguments_raw, arguments,
    result, outcome, failure_reason,
    applied_transaction_id, latency_ms
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- ListSimAgentToolCalls returns all tool calls for one turn, in execution
-- order. Used by the drill-down query "show me everything the agent did."
-- name: ListSimAgentToolCalls :many
SELECT turn_id, round_index, sequence,
       tool_name, recovered_name,
       arguments_raw, arguments,
       result, outcome, failure_reason,
       applied_transaction_id, latency_ms
FROM sim_agent_tool_calls
WHERE turn_id = $1
ORDER BY round_index, sequence;

-- InsertSimAgentTurnMessage is one message in the conversation transcript.
-- Batched via :copyfrom. Only written when sim_pools.record_full_messages
-- is true for the turn's pool.
-- name: InsertSimAgentTurnMessage :copyfrom
INSERT INTO sim_agent_turn_messages (
    turn_id, ordinal, role, content, tool_call_id, tool_calls
)
VALUES ($1, $2, $3, $4, $5, $6);

-- ListSimAgentTurnMessages returns the full conversation transcript for
-- one turn, in order.
-- name: ListSimAgentTurnMessages :many
SELECT turn_id, ordinal, role, content, tool_call_id, tool_calls
FROM sim_agent_turn_messages
WHERE turn_id = $1
ORDER BY ordinal;

-- GetSimPoolRecordFullMessages exposes just the gate column so the
-- activity doesn't have to fetch the whole sim_pools row to decide
-- whether to populate sim_agent_turn_messages.
-- name: GetSimPoolRecordFullMessages :one
SELECT record_full_messages FROM sim_pools WHERE id = $1;
