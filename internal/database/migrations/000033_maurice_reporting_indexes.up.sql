-- Indexes of the admin reporting lists (internal/mauricereport), which page
-- by immutable keys: an instance's users by (first_seen_at, id), and
-- conversations by (created_at, id), deleted ones included on request (the
-- owner index idx_maurice_conversations_user_updated covers only
-- undeleted conversations, by updated_at).
CREATE INDEX idx_app_users_instance_first_seen
    ON app_users (instance, first_seen_at DESC, id DESC);
CREATE INDEX idx_maurice_conversations_user_created
    ON maurice_conversations (user_id, created_at DESC, id DESC);
CREATE INDEX idx_maurice_conversations_created
    ON maurice_conversations (created_at DESC, id DESC);
