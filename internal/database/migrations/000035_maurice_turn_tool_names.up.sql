-- The tools a turn started, in call order, appended as each call starts, so
-- a poll on another replica lists them while the turn runs.
ALTER TABLE maurice_turns
    ADD COLUMN tool_names jsonb DEFAULT '[]'::jsonb NOT NULL
        CONSTRAINT maurice_turns_tool_names_array CHECK (jsonb_typeof(tool_names) = 'array');
