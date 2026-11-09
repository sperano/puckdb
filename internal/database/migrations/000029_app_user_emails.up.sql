-- The email of a user, as its login provider reports it: the Cloudflare
-- Access JWT email claim. NULL when the provider gives none (Authentik today).
-- A mutable profile snapshot like username, kept for linking users to the
-- Yahoo teams they manage.

ALTER TABLE app_users ADD COLUMN email text CHECK (email <> '');
