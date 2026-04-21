ALTER TYPE game_type ADD VALUE IF NOT EXISTS 'world_cup_pre_tournament' AFTER 'world_cup_2004';
ALTER TYPE game_type ADD VALUE IF NOT EXISTS 'lockout_lost' AFTER 'pwhl_showcase';
ALTER TYPE game_type ADD VALUE IF NOT EXISTS 'canada_cup' AFTER 'lockout_lost';
ALTER TYPE game_type ADD VALUE IF NOT EXISTS 'exhibition_overseas' AFTER 'canada_cup';
