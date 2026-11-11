DROP INDEX IF EXISTS user_notifications_dedupe_idx;
ALTER TABLE user_notifications DROP COLUMN IF EXISTS dedupe_key;
DROP TABLE IF EXISTS user_settings;
ALTER TABLE users DROP COLUMN IF EXISTS bio;
