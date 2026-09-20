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
    event_description = EXCLUDED.event_description
WHERE (shifts.game_id, shifts.player_id, shifts.team_id, shifts.period,
       shifts.start_time, shifts.end_time, shifts.duration,
       shifts.shift_number, shifts.type_code, shifts.detail_code,
       shifts.event_number, shifts.event_description)
      IS DISTINCT FROM
      (EXCLUDED.game_id, EXCLUDED.player_id, EXCLUDED.team_id, EXCLUDED.period,
       EXCLUDED.start_time, EXCLUDED.end_time, EXCLUDED.duration,
       EXCLUDED.shift_number, EXCLUDED.type_code, EXCLUDED.detail_code,
       EXCLUDED.event_number, EXCLUDED.event_description);
