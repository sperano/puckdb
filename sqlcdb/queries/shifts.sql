-- name: UpsertShiftBatch :batchexec
INSERT INTO shifts (
    id, game_id, player_id, team_id, period,
    start_time, end_time, duration,
    shift_number, type_code, detail_code,
    event_number, event_description
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11,
    $12, $13
)
ON CONFLICT (id) DO UPDATE SET
    game_id = EXCLUDED.game_id,
    player_id = EXCLUDED.player_id,
    team_id = EXCLUDED.team_id,
    period = EXCLUDED.period,
    start_time = EXCLUDED.start_time,
    end_time = EXCLUDED.end_time,
    duration = EXCLUDED.duration,
    shift_number = EXCLUDED.shift_number,
    type_code = EXCLUDED.type_code,
    detail_code = EXCLUDED.detail_code,
    event_number = EXCLUDED.event_number,
    event_description = EXCLUDED.event_description;
