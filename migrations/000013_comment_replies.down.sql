DROP INDEX IF EXISTS comments_replies_idx;
DROP INDEX IF EXISTS comments_content_top_idx;
ALTER TABLE comments DROP COLUMN IF EXISTS reply_to_user_id;
ALTER TABLE comments DROP COLUMN IF EXISTS parent_id;
