-- The daily_turn_done duplicates the up migration deleted are not restored:
-- they were interchangeable copies of the row that stays.
DROP INDEX ux_sim_transactions_daily_turn_done;

DROP INDEX ux_sim_transactions_draft_pick;
