-- name: GetNHLDayImport :one
SELECT importer_version, inputs_hash
FROM nhl_day_imports
WHERE season = sqlc.arg(season) AND date = sqlc.arg(date);

-- name: UpsertNHLDayImport :exec
INSERT INTO nhl_day_imports (season, date, importer_version, inputs_hash, input_files, imported_at)
VALUES (sqlc.arg(season), sqlc.arg(date), sqlc.arg(importer_version), sqlc.arg(inputs_hash), sqlc.arg(input_files), now())
ON CONFLICT (season, date) DO UPDATE SET
    importer_version = EXCLUDED.importer_version,
    inputs_hash = EXCLUDED.inputs_hash,
    input_files = EXCLUDED.input_files,
    imported_at = EXCLUDED.imported_at;
