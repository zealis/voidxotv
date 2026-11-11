-- ── 评论楼中楼（UI 规范 §6.5：回复上下文、缩进与「回复 @某人」标识）────
-- parent_id 采用「压平到顶层」的两级结构：回复永远指向所属的顶层评论，
-- 回复的回复也挂到同一顶层下，前端以缩进 + reply_to 标识呈现讨论线。
-- reply_to_user_id 记录「回复 @某人」的目标（可以是同线程里另一条回复的作者）。

ALTER TABLE comments ADD COLUMN IF NOT EXISTS parent_id BIGINT REFERENCES comments(id) ON DELETE CASCADE;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS reply_to_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

-- 顶层评论列表：按内容倒序的游标分页。
CREATE INDEX IF NOT EXISTS comments_content_top_idx
    ON comments (content_id, id DESC) WHERE status = 'visible' AND parent_id IS NULL;
-- 线程内回复：按线程正序读取。
CREATE INDEX IF NOT EXISTS comments_replies_idx
    ON comments (parent_id, id) WHERE status = 'visible' AND parent_id IS NOT NULL;
