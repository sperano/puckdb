-- Tool arguments are provider output and may not be valid JSON. The raw text
-- is the lossless source; the nullable parsed copies were never queried and
-- are derivable when the text is valid jsonb.
ALTER TABLE sim_agent_tool_calls
    DROP COLUMN arguments;

ALTER TABLE maurice_tool_calls
    DROP COLUMN arguments;
