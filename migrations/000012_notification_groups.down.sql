DROP INDEX IF EXISTS user_notifications_list_idx;
ALTER TABLE user_notifications DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE user_notifications DROP COLUMN IF EXISTS actor_id;
ALTER TABLE user_notifications DROP CONSTRAINT IF EXISTS user_notifications_category_check;
ALTER TABLE user_notifications DROP COLUMN IF EXISTS category;
