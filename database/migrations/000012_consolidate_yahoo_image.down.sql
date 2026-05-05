ALTER TABLE players RENAME COLUMN yahoo_image TO yahoo_image_large;
ALTER TABLE players ADD COLUMN yahoo_image_small  TEXT NOT NULL DEFAULT '';
ALTER TABLE players ADD COLUMN yahoo_image_medium TEXT NOT NULL DEFAULT '';
