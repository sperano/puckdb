-- One draft_pick per draft slot and one daily_turn_done marker per
-- (pool, agent, day), enforced by the commit itself.
--
-- DraftPick and ManageRoster probe for their marker before calling the
-- model, outside the commit transaction. An attempt still running after its
-- StartToClose timeout overlaps the retry Temporal starts, so both can pass
-- the probe; without these indexes both commits land (a second pick, a second
-- day marker), and the second commit's turn telemetry deletes the first's.
-- Each commit now inserts its marker first, so the loser waits on the
-- winner's index entry, fails with a unique violation and rolls back before
-- any other write; the activity then records its spend and returns the
-- committed result (internal/worker/simulation/turn_markers.go).

-- A daily_turn_done row carries nothing but its coordinate, so duplicates
-- are interchangeable: keep the first.
DELETE FROM sim_transactions d
 USING sim_transactions k
 WHERE d.type = 'daily_turn_done'
   AND k.type = 'daily_turn_done'
   AND d.pool_id = k.pool_id
   AND d.agent_id = k.agent_id
   AND d.date = k.date
   AND d.id > k.id;

-- A duplicate draft_pick is a second player drafted into one slot, with its
-- own roster row and turn telemetry; which pick stands is not this
-- migration's call. Refuse, naming every slot, until an operator has removed
-- the extra picks (see the wiki page puckdb/simulation-turn-idempotency.md).
DO $$
DECLARE
    duplicates text;
BEGIN
    SELECT string_agg(format('pool %s agent %s round %s pick %s', pool_id, agent_id, round, pick), '; '
                      ORDER BY pool_id, agent_id, round, pick)
      INTO duplicates
      FROM (SELECT pool_id, agent_id, round, pick
              FROM sim_transactions
             WHERE type = 'draft_pick'
             GROUP BY pool_id, agent_id, round, pick
            HAVING count(*) > 1) d;
    IF duplicates IS NOT NULL THEN
        RAISE EXCEPTION 'sim_transactions has duplicate draft picks: %', duplicates
            USING HINT = 'Keep one draft_pick row (and its sim_rosters row) per slot, then rerun the migration.';
    END IF;
END
$$;

CREATE UNIQUE INDEX ux_sim_transactions_draft_pick
    ON sim_transactions (pool_id, agent_id, round, pick)
    WHERE type = 'draft_pick';

CREATE UNIQUE INDEX ux_sim_transactions_daily_turn_done
    ON sim_transactions (pool_id, agent_id, date)
    WHERE type = 'daily_turn_done';
