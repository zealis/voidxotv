-- ── 通知分组与列表管理（UI 规范 §9.1）────────────────────────
-- category 固化 §9.1 的三个首期分组（互动 / 关注 / 系统）：
-- 入库时按 kind 推导，列表过滤与未读分桶不再每次重算；
-- kind 仍保留细分值（moderation / appeal / report / account / system）供标签显示。
-- actor_id 记录触发通知的用户（点赞者 / 评论者 / 关注者），列表头像由此批量补全；
-- 系统通知（审核结论、申诉结果、举报反馈）没有来源用户，保持 NULL。
-- deleted_at 是用户主动删除的软删标记（§9.1 左滑删除）：列表与未读计数一律排除。

ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'system';
ALTER TABLE user_notifications DROP CONSTRAINT IF EXISTS user_notifications_category_check;
ALTER TABLE user_notifications ADD CONSTRAINT user_notifications_category_check
    CHECK (category IN ('interaction', 'follow', 'system'));
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS actor_id BIGINT
    REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

-- 存量数据按 dispatch 的推导规则回填；重复执行结果不变。
UPDATE user_notifications SET category = CASE kind
    WHEN 'interaction' THEN 'interaction'
    WHEN 'follow'      THEN 'follow'
    ELSE 'system'
END;

-- 列表主查询是 user_id + deleted_at IS NULL + 按 id 倒序的游标分页。
CREATE INDEX IF NOT EXISTS user_notifications_list_idx
    ON user_notifications (user_id, id DESC) WHERE deleted_at IS NULL;
