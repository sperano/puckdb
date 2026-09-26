-- Speeds the projection skater history/evaluation queries' per-game faceoff
-- aggregation (see ListProjectionSkaterHistory / ListProjectionSkaterEvaluationData
-- in internal/sqlcdb/queries/projections.sql). A partial index scoped to
-- faceoff rows, keyed and covering exactly the columns that query groups and
-- selects, lets Postgres satisfy it with an index-only scan of the ~74k
-- faceoffs per season instead of a bitmap heap scan over play_events'
-- ~7.8M rows via the general type_desc_key index.
CREATE INDEX idx_play_events_faceoff_players
    ON play_events (game_id)
    INCLUDE (winning_player_id, losing_player_id)
    WHERE type_desc_key = 'faceoff';
