-- The Yahoo email the API's members file (--maurice-members) gave the user's
-- login email at their last authenticated request, matched against
-- yahoo_team_managers.email besides the login email (NULL: no entry, or an
-- entry without one). Stored so `puckdb user unlink-team`, which never reads
-- the file, sees the same emails the last login synced the links from.
ALTER TABLE app_users ADD COLUMN member_yahoo_email text;
