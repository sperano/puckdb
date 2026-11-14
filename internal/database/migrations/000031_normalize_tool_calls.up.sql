-- Tool calls are queryable rows linked to their assistant message. Raw
-- arguments preserve malformed provider output; arguments is the nullable
-- jsonb projection for valid payloads.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM maurice_messages
        WHERE tool_calls IS NOT NULL AND jsonb_typeof(tool_calls) <> 'array'
    ) THEN
        RAISE EXCEPTION 'maurice_messages.tool_calls contains a non-array value';
    END IF;
    IF EXISTS (
        SELECT 1 FROM sim_agent_turn_messages
        WHERE tool_calls IS NOT NULL AND jsonb_typeof(tool_calls) <> 'array'
    ) THEN
        RAISE EXCEPTION 'sim_agent_turn_messages.tool_calls contains a non-array value';
    END IF;
END $$;

ALTER TABLE maurice_tool_calls
    ADD COLUMN message_id uuid REFERENCES maurice_messages(id) ON DELETE SET NULL,
    ADD COLUMN tool_type text NOT NULL DEFAULT 'function' CHECK (tool_type <> '');

ALTER TABLE maurice_tool_calls
    ALTER COLUMN llm_call_id DROP NOT NULL,
    ALTER COLUMN status DROP NOT NULL;

WITH assistant_messages AS (
    SELECT m.*,
           row_number() OVER (PARTITION BY m.turn_id ORDER BY m.message_number) - 1 AS tool_round
    FROM maurice_messages m
    WHERE jsonb_typeof(m.tool_calls) = 'array'
), message_calls AS (
    SELECT m.id AS message_id,
           m.turn_id,
           m.tool_round,
           call.ordinality - 1 AS sequence_number,
           NULLIF(call.value->>'id', '') AS provider_tool_call_id,
           COALESCE(NULLIF(call.value->>'type', ''), 'function') AS tool_type
    FROM assistant_messages m
    CROSS JOIN LATERAL jsonb_array_elements(m.tool_calls)
        WITH ORDINALITY AS call(value, ordinality)
)
UPDATE maurice_tool_calls stored
SET message_id = source.message_id,
    tool_type = source.tool_type
FROM message_calls source, maurice_llm_calls call_row
WHERE stored.turn_id = source.turn_id
  AND stored.llm_call_id = call_row.id
  AND call_row.round_number = source.tool_round
  AND stored.sequence_number = source.sequence_number
  AND stored.provider_tool_call_id IS NOT DISTINCT FROM source.provider_tool_call_id;

WITH assistant_messages AS (
    SELECT m.*
    FROM maurice_messages m
    WHERE jsonb_typeof(m.tool_calls) = 'array'
), message_calls AS (
    SELECT m.id AS message_id,
           m.turn_id,
           call.ordinality - 1 AS sequence_number,
           NULLIF(call.value->>'id', '') AS provider_tool_call_id,
           COALESCE(NULLIF(call.value->>'type', ''), 'function') AS tool_type,
           COALESCE(call.value->'function'->>'name', '') AS tool_name,
           COALESCE(call.value->'function'->>'arguments', '') AS arguments_raw
    FROM assistant_messages m
    CROSS JOIN LATERAL jsonb_array_elements(m.tool_calls)
        WITH ORDINALITY AS call(value, ordinality)
)
INSERT INTO maurice_tool_calls (
    turn_id, message_id, sequence_number, provider_tool_call_id, tool_type,
    tool_name, arguments, arguments_raw
)
SELECT source.turn_id,
       source.message_id,
       source.sequence_number,
       source.provider_tool_call_id,
       source.tool_type,
       source.tool_name,
       CASE WHEN pg_input_is_valid(source.arguments_raw, 'jsonb')
            THEN source.arguments_raw::jsonb END,
       source.arguments_raw
FROM message_calls source
WHERE NOT EXISTS (
    SELECT 1
    FROM maurice_tool_calls stored
    WHERE stored.message_id = source.message_id
      AND stored.sequence_number = source.sequence_number
);

ALTER TABLE maurice_tool_calls
    ADD CONSTRAINT maurice_tool_calls_message_sequence_key
        UNIQUE (message_id, sequence_number);

ALTER TABLE maurice_messages
    ADD COLUMN result_tool_call_id bigint REFERENCES maurice_tool_calls(id) ON DELETE SET NULL;

WITH candidates AS (
    SELECT result_message.id AS result_message_id,
           tool_call.id AS tool_call_id,
           row_number() OVER (
               PARTITION BY result_message.id
               ORDER BY call_message.message_number DESC
           ) AS candidate_number
    FROM maurice_messages result_message
    JOIN maurice_tool_calls tool_call
      ON tool_call.turn_id = result_message.turn_id
     AND tool_call.provider_tool_call_id = result_message.tool_call_id
    JOIN maurice_messages call_message ON call_message.id = tool_call.message_id
    WHERE call_message.message_number < result_message.message_number
)
UPDATE maurice_messages result_message
SET result_tool_call_id = candidate.tool_call_id
FROM candidates candidate
WHERE result_message.id = candidate.result_message_id
  AND candidate.candidate_number = 1;

ALTER TABLE maurice_messages
    DROP COLUMN tool_calls,
    DROP COLUMN tool_call_id;

ALTER TABLE sim_agent_tool_calls
    ADD COLUMN message_ordinal integer,
    ADD COLUMN provider_tool_call_id text,
    ADD COLUMN tool_type text NOT NULL DEFAULT 'function' CHECK (tool_type <> '');

WITH assistant_messages AS (
    SELECT m.turn_id,
           m.ordinal AS message_ordinal,
           row_number() OVER (PARTITION BY m.turn_id ORDER BY m.ordinal) - 1 AS round_index,
           m.tool_calls
    FROM sim_agent_turn_messages m
    WHERE jsonb_typeof(m.tool_calls) = 'array'
), message_calls AS (
    SELECT m.turn_id,
           m.message_ordinal,
           m.round_index,
           call.ordinality - 1 AS sequence,
           NULLIF(call.value->>'id', '') AS provider_tool_call_id,
           COALESCE(NULLIF(call.value->>'type', ''), 'function') AS tool_type
    FROM assistant_messages m
    CROSS JOIN LATERAL jsonb_array_elements(m.tool_calls)
        WITH ORDINALITY AS call(value, ordinality)
)
UPDATE sim_agent_tool_calls stored
SET message_ordinal = source.message_ordinal,
    provider_tool_call_id = source.provider_tool_call_id,
    tool_type = source.tool_type
FROM message_calls source
WHERE stored.turn_id = source.turn_id
  AND stored.round_index = source.round_index
  AND stored.sequence = source.sequence;

ALTER TABLE sim_agent_tool_calls
    ADD CONSTRAINT sim_agent_tool_calls_message_fkey
        FOREIGN KEY (turn_id, message_ordinal)
        REFERENCES sim_agent_turn_messages(turn_id, ordinal);

ALTER TABLE sim_agent_turn_messages
    ADD COLUMN result_tool_round_index integer,
    ADD COLUMN result_tool_sequence integer,
    ADD CONSTRAINT sim_agent_turn_messages_result_tool_fkey
        FOREIGN KEY (turn_id, result_tool_round_index, result_tool_sequence)
        REFERENCES sim_agent_tool_calls(turn_id, round_index, sequence);

WITH candidates AS (
    SELECT result_message.turn_id,
           result_message.ordinal,
           tool_call.round_index,
           tool_call.sequence,
           row_number() OVER (
               PARTITION BY result_message.turn_id, result_message.ordinal
               ORDER BY tool_call.message_ordinal DESC
           ) AS candidate_number
    FROM sim_agent_turn_messages result_message
    JOIN sim_agent_tool_calls tool_call
      ON tool_call.turn_id = result_message.turn_id
     AND tool_call.provider_tool_call_id = result_message.tool_call_id
    WHERE tool_call.message_ordinal < result_message.ordinal
)
UPDATE sim_agent_turn_messages result_message
SET result_tool_round_index = candidate.round_index,
    result_tool_sequence = candidate.sequence
FROM candidates candidate
WHERE result_message.turn_id = candidate.turn_id
  AND result_message.ordinal = candidate.ordinal
  AND candidate.candidate_number = 1;

ALTER TABLE sim_agent_turn_messages
    DROP COLUMN tool_calls,
    DROP COLUMN tool_call_id;
