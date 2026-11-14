ALTER TABLE sim_agent_tool_calls
    ADD COLUMN arguments jsonb;

UPDATE sim_agent_tool_calls
SET arguments = arguments_raw::jsonb
WHERE pg_input_is_valid(arguments_raw, 'jsonb');

ALTER TABLE maurice_tool_calls
    ADD COLUMN arguments jsonb;

UPDATE maurice_tool_calls
SET arguments = arguments_raw::jsonb
WHERE pg_input_is_valid(arguments_raw, 'jsonb');
