-- name: UpsertPlayEventBatch :batchexec
INSERT INTO play_events (
    game_id, event_id, period, period_type, time_in_period, time_remaining,
    situation_code, home_team_defending_side, type_desc_key, sort_order,
    x_coord, y_coord, zone_code, event_owner_team_id,
    shot_type, shooting_player_id, goalie_in_net_id,
    blocking_player_id,
    scoring_player_id, scoring_player_total,
    assist1_player_id, assist1_player_total,
    assist2_player_id, assist2_player_total,
    away_score, home_score,
    highlight_clip_id, highlight_clip_url, discrete_clip_id,
    penalty_type_code, penalty_desc_key, penalty_duration,
    committed_by_player_id, drawn_by_player_id,
    hitting_player_id, hittee_player_id,
    winning_player_id, losing_player_id,
    player_id, reason, away_sog, home_sog
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17,
    $18,
    $19, $20,
    $21, $22,
    $23, $24,
    $25, $26,
    $27, $28, $29,
    $30, $31, $32,
    $33, $34,
    $35, $36,
    $37, $38,
    $39, $40, $41, $42
)
ON CONFLICT (game_id, event_id) DO UPDATE SET
    period = EXCLUDED.period,
    period_type = EXCLUDED.period_type,
    time_in_period = EXCLUDED.time_in_period,
    time_remaining = EXCLUDED.time_remaining,
    situation_code = EXCLUDED.situation_code,
    home_team_defending_side = EXCLUDED.home_team_defending_side,
    type_desc_key = EXCLUDED.type_desc_key,
    sort_order = EXCLUDED.sort_order,
    x_coord = EXCLUDED.x_coord,
    y_coord = EXCLUDED.y_coord,
    zone_code = EXCLUDED.zone_code,
    event_owner_team_id = EXCLUDED.event_owner_team_id,
    shot_type = EXCLUDED.shot_type,
    shooting_player_id = EXCLUDED.shooting_player_id,
    goalie_in_net_id = EXCLUDED.goalie_in_net_id,
    blocking_player_id = EXCLUDED.blocking_player_id,
    scoring_player_id = EXCLUDED.scoring_player_id,
    scoring_player_total = EXCLUDED.scoring_player_total,
    assist1_player_id = EXCLUDED.assist1_player_id,
    assist1_player_total = EXCLUDED.assist1_player_total,
    assist2_player_id = EXCLUDED.assist2_player_id,
    assist2_player_total = EXCLUDED.assist2_player_total,
    away_score = EXCLUDED.away_score,
    home_score = EXCLUDED.home_score,
    highlight_clip_id = EXCLUDED.highlight_clip_id,
    highlight_clip_url = EXCLUDED.highlight_clip_url,
    discrete_clip_id = EXCLUDED.discrete_clip_id,
    penalty_type_code = EXCLUDED.penalty_type_code,
    penalty_desc_key = EXCLUDED.penalty_desc_key,
    penalty_duration = EXCLUDED.penalty_duration,
    committed_by_player_id = EXCLUDED.committed_by_player_id,
    drawn_by_player_id = EXCLUDED.drawn_by_player_id,
    hitting_player_id = EXCLUDED.hitting_player_id,
    hittee_player_id = EXCLUDED.hittee_player_id,
    winning_player_id = EXCLUDED.winning_player_id,
    losing_player_id = EXCLUDED.losing_player_id,
    player_id = EXCLUDED.player_id,
    reason = EXCLUDED.reason,
    away_sog = EXCLUDED.away_sog,
    home_sog = EXCLUDED.home_sog
WHERE (play_events.period, play_events.period_type,
       play_events.time_in_period, play_events.time_remaining,
       play_events.situation_code, play_events.home_team_defending_side,
       play_events.type_desc_key, play_events.sort_order,
       play_events.x_coord, play_events.y_coord, play_events.zone_code,
       play_events.event_owner_team_id,
       play_events.shot_type, play_events.shooting_player_id,
       play_events.goalie_in_net_id, play_events.blocking_player_id,
       play_events.scoring_player_id, play_events.scoring_player_total,
       play_events.assist1_player_id, play_events.assist1_player_total,
       play_events.assist2_player_id, play_events.assist2_player_total,
       play_events.away_score, play_events.home_score,
       play_events.highlight_clip_id, play_events.highlight_clip_url,
       play_events.discrete_clip_id,
       play_events.penalty_type_code, play_events.penalty_desc_key,
       play_events.penalty_duration,
       play_events.committed_by_player_id, play_events.drawn_by_player_id,
       play_events.hitting_player_id, play_events.hittee_player_id,
       play_events.winning_player_id, play_events.losing_player_id,
       play_events.player_id, play_events.reason,
       play_events.away_sog, play_events.home_sog)
      IS DISTINCT FROM
      (EXCLUDED.period, EXCLUDED.period_type,
       EXCLUDED.time_in_period, EXCLUDED.time_remaining,
       EXCLUDED.situation_code, EXCLUDED.home_team_defending_side,
       EXCLUDED.type_desc_key, EXCLUDED.sort_order,
       EXCLUDED.x_coord, EXCLUDED.y_coord, EXCLUDED.zone_code,
       EXCLUDED.event_owner_team_id,
       EXCLUDED.shot_type, EXCLUDED.shooting_player_id,
       EXCLUDED.goalie_in_net_id, EXCLUDED.blocking_player_id,
       EXCLUDED.scoring_player_id, EXCLUDED.scoring_player_total,
       EXCLUDED.assist1_player_id, EXCLUDED.assist1_player_total,
       EXCLUDED.assist2_player_id, EXCLUDED.assist2_player_total,
       EXCLUDED.away_score, EXCLUDED.home_score,
       EXCLUDED.highlight_clip_id, EXCLUDED.highlight_clip_url,
       EXCLUDED.discrete_clip_id,
       EXCLUDED.penalty_type_code, EXCLUDED.penalty_desc_key,
       EXCLUDED.penalty_duration,
       EXCLUDED.committed_by_player_id, EXCLUDED.drawn_by_player_id,
       EXCLUDED.hitting_player_id, EXCLUDED.hittee_player_id,
       EXCLUDED.winning_player_id, EXCLUDED.losing_player_id,
       EXCLUDED.player_id, EXCLUDED.reason,
       EXCLUDED.away_sog, EXCLUDED.home_sog);

-- name: GetGamePlayEvents :many
-- Play events for a game, ordered chronologically by sort_order.
-- Optional filters: type_desc_keys (e.g. {'shot-on-goal','goal'}) and period.
-- Pass NULL / empty array to disable a filter. `limit` caps the row count
-- (NULL = no cap); combine with type_desc_keys to get e.g. the first 2 shots.
SELECT *
FROM play_events
WHERE game_id = @game_id::bigint
  AND (sqlc.narg('period')::int IS NULL OR period = sqlc.narg('period'))
  AND (cardinality(@type_desc_keys::text[]) = 0
       OR type_desc_key::text = ANY(@type_desc_keys::text[]))
ORDER BY sort_order
LIMIT sqlc.narg('limit')::int;

-- name: GetFirstMatchingEventPerTeam :many
-- For each (game, team) in the given set, return the earliest play event
-- (by sort_order) whose type_desc_key matches the supplied prerequisite filter.
-- The type_desc_keys filter is applied BEFORE the per-team earliest selection,
-- so e.g. type_desc_keys = {'shot-on-goal','goal'} returns each team's first
-- SHOT (not its first event-of-any-kind that happens to be a shot).
-- Caller resolves scope (season → game_ids) before calling. Must pass a
-- non-empty type_desc_keys array.
SELECT DISTINCT ON (game_id, event_owner_team_id) *
FROM play_events
WHERE game_id = ANY(@game_ids::bigint[])
  AND event_owner_team_id IS NOT NULL
  AND cardinality(@type_desc_keys::text[]) > 0
  AND type_desc_key::text = ANY(@type_desc_keys::text[])
ORDER BY game_id, event_owner_team_id, sort_order;
