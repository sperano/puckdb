ALTER TABLE maurice_turns
    ADD COLUMN warnings jsonb DEFAULT '[]'::jsonb NOT NULL;
