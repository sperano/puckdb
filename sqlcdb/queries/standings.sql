-- =============================================================================
-- Standings Snapshots Queries
-- =============================================================================

-- name: UpsertStandingsSnapshotBatch :batchexec
INSERT INTO standings_snapshots (
    season, date, team_abbrev,
    wins, losses, ot_losses, points,
    division_abbrev, division_name,
    conference_abbrev, conference_name
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (season, date, team_abbrev) DO UPDATE SET
    wins = EXCLUDED.wins,
    losses = EXCLUDED.losses,
    ot_losses = EXCLUDED.ot_losses,
    points = EXCLUDED.points,
    division_abbrev = EXCLUDED.division_abbrev,
    division_name = EXCLUDED.division_name,
    conference_abbrev = EXCLUDED.conference_abbrev,
    conference_name = EXCLUDED.conference_name
WHERE (standings_snapshots.wins, standings_snapshots.losses,
       standings_snapshots.ot_losses, standings_snapshots.points,
       standings_snapshots.division_abbrev, standings_snapshots.division_name,
       standings_snapshots.conference_abbrev, standings_snapshots.conference_name)
      IS DISTINCT FROM
      (EXCLUDED.wins, EXCLUDED.losses,
       EXCLUDED.ot_losses, EXCLUDED.points,
       EXCLUDED.division_abbrev, EXCLUDED.division_name,
       EXCLUDED.conference_abbrev, EXCLUDED.conference_name);

-- name: GetStandingsSnapshotsByDate :many
SELECT * FROM standings_snapshots
WHERE date = $1
ORDER BY points DESC, wins DESC;

-- name: GetStandingsSnapshotsBySeasonAndDate :many
SELECT * FROM standings_snapshots
WHERE season = $1 AND date = $2
ORDER BY points DESC, wins DESC;

-- name: GetStandingsSnapshotsBySeason :many
SELECT * FROM standings_snapshots
WHERE season = $1
ORDER BY date, team_abbrev;

-- name: GetStandingsSnapshotsByTeam :many
SELECT * FROM standings_snapshots
WHERE season = $1 AND team_abbrev = $2
ORDER BY date;

-- name: CountStandingsSnapshots :one
SELECT COUNT(*) FROM standings_snapshots;

-- name: CountStandingsSnapshotsBySeason :one
SELECT COUNT(*) FROM standings_snapshots WHERE season = $1;
