ALTER TABLE sim_agent_turn_messages
    ADD COLUMN tool_call_id text,
    ADD COLUMN tool_calls jsonb;

UPDATE sim_agent_turn_messages result_message
SET tool_call_id = tool_call.provider_tool_call_id
FROM sim_agent_tool_calls tool_call
WHERE result_message.turn_id = tool_call.turn_id
  AND result_message.result_tool_round_index = tool_call.round_index
  AND result_message.result_tool_sequence = tool_call.sequence;

WITH calls AS (
    SELECT message_ordinal,
           turn_id,
           jsonb_agg(
               jsonb_build_object(
                   'id', COALESCE(provider_tool_call_id, ''),
                   'type', tool_type,
                   'function', jsonb_build_object(
                       'name', tool_name,
                       'arguments', arguments_raw
                   )
               ) ORDER BY sequence
           ) AS tool_calls
    FROM sim_agent_tool_calls
    WHERE message_ordinal IS NOT NULL
    GROUP BY turn_id, message_ordinal
)
UPDATE sim_agent_turn_messages message
SET tool_calls = calls.tool_calls
FROM calls
WHERE message.turn_id = calls.turn_id
  AND message.ordinal = calls.message_ordinal;

ALTER TABLE sim_agent_turn_messages
    DROP CONSTRAINT sim_agent_turn_messages_result_tool_fkey,
    DROP COLUMN result_tool_round_index,
    DROP COLUMN result_tool_sequence;

ALTER TABLE sim_agent_tool_calls
    DROP CONSTRAINT sim_agent_tool_calls_message_fkey,
    DROP COLUMN message_ordinal,
    DROP COLUMN provider_tool_call_id,
    DROP COLUMN tool_type;

ALTER TABLE maurice_messages
    ADD COLUMN tool_call_id text,
    ADD COLUMN tool_calls jsonb;

UPDATE maurice_messages result_message
SET tool_call_id = tool_call.provider_tool_call_id
FROM maurice_tool_calls tool_call
WHERE result_message.result_tool_call_id = tool_call.id;

WITH calls AS (
    SELECT message_id,
           jsonb_agg(
               jsonb_build_object(
                   'id', COALESCE(provider_tool_call_id, ''),
                   'type', tool_type,
                   'function', jsonb_build_object(
                       'name', tool_name,
                       'arguments', arguments_raw
                   )
               ) ORDER BY sequence_number
           ) AS tool_calls
    FROM maurice_tool_calls
    WHERE message_id IS NOT NULL
    GROUP BY message_id
)
UPDATE maurice_messages message
SET tool_calls = calls.tool_calls
FROM calls
WHERE message.id = calls.message_id;

ALTER TABLE maurice_messages
    DROP COLUMN result_tool_call_id;

DELETE FROM maurice_tool_calls
WHERE llm_call_id IS NULL;

ALTER TABLE maurice_tool_calls
    DROP CONSTRAINT maurice_tool_calls_message_sequence_key,
    DROP COLUMN message_id,
    DROP COLUMN tool_type;

ALTER TABLE maurice_tool_calls
    ALTER COLUMN llm_call_id SET NOT NULL,
    ALTER COLUMN status SET NOT NULL;
