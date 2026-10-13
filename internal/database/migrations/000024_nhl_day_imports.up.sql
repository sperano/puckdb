-- One row per NHL day that ImportNHLSeasonWorkflow imported successfully:
-- a fingerprint of the cached files the day import reads (each file's path,
-- size and modification time, or its absence) and the importer version that
-- read them. A rerun skips a day whose fingerprint and importer version are
-- both unchanged; --reimport-days ignores this table. Dropping the database
-- drops the records with the data they describe.
CREATE TABLE nhl_day_imports (
    season integer NOT NULL,
    date date NOT NULL,
    importer_version integer NOT NULL,
    inputs_hash text NOT NULL,
    input_files integer NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (season, date),
    CHECK (importer_version > 0),
    CHECK (input_files > 0)
);
