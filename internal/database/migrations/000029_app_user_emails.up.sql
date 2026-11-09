-- The email of a user, as its login provider reports it: the Cloudflare
-- Access JWT email claim. NULL when the provider gives none (Authentik today).
-- A mutable profile snapshot like username, kept for linking users to the
-- Yahoo teams they manage.
--
-- Rollout: with 000028 (see its header); the login upsert of the build that
-- ships it writes this column, so it fails until this has run.

ALTER TABLE app_users ADD COLUMN email text CHECK (email <> '');
