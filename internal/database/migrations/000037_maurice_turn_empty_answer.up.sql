-- A turn whose final response had no text, even after the retry asking for
-- it, fails as 'empty_answer' instead of succeeding with no content.
ALTER TABLE maurice_turns DROP CONSTRAINT maurice_turns_error_class_check;
ALTER TABLE maurice_turns ADD CONSTRAINT maurice_turns_error_class_check
    CHECK (error_class IN
        ('timeout', 'cancelled', 'provider_error', 'tool_error', 'internal_error', 'abandoned', 'empty_answer'));
