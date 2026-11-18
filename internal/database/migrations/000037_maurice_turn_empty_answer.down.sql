-- The previous class set has no 'empty_answer'; such turns become internal
-- errors, still failed.
UPDATE maurice_turns SET error_class = 'internal_error' WHERE error_class = 'empty_answer';
ALTER TABLE maurice_turns DROP CONSTRAINT maurice_turns_error_class_check;
ALTER TABLE maurice_turns ADD CONSTRAINT maurice_turns_error_class_check
    CHECK (error_class IN
        ('timeout', 'cancelled', 'provider_error', 'tool_error', 'internal_error', 'abandoned'));
