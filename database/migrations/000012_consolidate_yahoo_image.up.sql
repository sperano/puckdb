-- Yahoo only exposes a single source size for player images (the players_l
-- variant). The small/medium columns were never populated, so drop them and
-- rename the surviving column for clarity.
ALTER TABLE players DROP COLUMN yahoo_image_small;
ALTER TABLE players DROP COLUMN yahoo_image_medium;
ALTER TABLE players RENAME COLUMN yahoo_image_large TO yahoo_image;
