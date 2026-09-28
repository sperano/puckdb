-- even_strength_segments stored one row per (game, period, atomic segment,
-- skater) — about 8,000 rows per game, 9.7 GB in production — and its
-- per-game rebuild dominated import I/O (WALInsert/BufferContent
-- contention). Its only reader, ListProjectionSkaterLinemateContext, only
-- ever needed per-game totals: shared even-strength TOI between two
-- teammates, and a skater's own even-strength TOI and points. Replacing the
-- segment table with two small per-game total tables keeps the same segment
-- derivation (still computed in the rebuild query, just not stored) while
-- cutting the per-game row count by roughly two orders of magnitude.
DROP TABLE even_strength_segments;

-- One row per (game, skater, on-ice teammate) with their shared
-- even-strength seconds, both directions stored (player->teammate and
-- teammate->player) so a reader never needs a self-join. Same team only.
CREATE TABLE even_strength_pair_toi (
    game_id bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id bigint NOT NULL,
    teammate_id bigint NOT NULL,
    shared_toi_seconds integer NOT NULL,
    PRIMARY KEY (game_id, player_id, teammate_id),
    CHECK (shared_toi_seconds > 0)
);

-- One row per (game, skater): the skater's own even-strength time on ice and
-- even-strength points (goal, assist1, or assist2 on a goal whose situation
-- code is equal-strength 3-5 skaters with both goalies in, credited when the
-- skater's own even-strength segment covers the goal's clock time).
CREATE TABLE even_strength_skater_games (
    game_id bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id bigint NOT NULL,
    team_id bigint NOT NULL,
    toi_seconds integer NOT NULL,
    points integer NOT NULL DEFAULT 0,
    PRIMARY KEY (game_id, player_id),
    CHECK (toi_seconds > 0)
);
