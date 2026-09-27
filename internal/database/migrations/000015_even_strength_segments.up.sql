-- ListProjectionSkaterLinemateContext used to split every shift chart into
-- even-strength segments at query time; over the full shifts table the
-- planner could not estimate the derived CTEs and never finished. The shift
-- chart import now rebuilds each game's segments here instead (see
-- InsertEvenStrengthSegmentsForGame), and the linemate query reads them.
--
-- One row per (game, period, atomic half-open segment, skater): segments are
-- the spans between consecutive shift boundaries of a period, kept only when
-- both clubs have the same 3-5 skaters on the ice and exactly one goalie
-- each. Goalies are not stored. Season, game type, and game state are not
-- stored either: readers join games, so every imported game with shifts has
-- segments regardless of its type.
--
-- Segments within a (game, period) are keyed by start_second, and a player
-- has one game_skater_stats row (so one club) per game, which makes the key
-- unique. Its order serves the per-game rebuild (game_id) and the goal
-- attribution lookup (game_id, period, player_id, start_second < goal
-- second); the teammate self-join reads whole seasons, so it scans and
-- sorts or hashes rather than probing an index.
CREATE TABLE even_strength_segments (
    game_id bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    period integer NOT NULL,
    start_second integer NOT NULL,
    end_second integer NOT NULL,
    team_id bigint NOT NULL,
    player_id bigint NOT NULL,
    PRIMARY KEY (game_id, period, player_id, start_second),
    CHECK (start_second < end_second)
);
