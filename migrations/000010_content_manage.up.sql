-- ── 作品管理（创作者中心）：软删除 ─────────────────────────────
-- 不物理删除：统计、举报、申诉都要保留引用；publish_state='deleted' 后
-- 所有公开查询（feed / 落地页 / 搜索）自然查不到，作者侧列表也需排除。

ALTER TABLE contents DROP CONSTRAINT IF EXISTS contents_publish_state_check;
ALTER TABLE contents ADD CONSTRAINT contents_publish_state_check
    CHECK (publish_state IN ('draft', 'scheduled', 'published', 'deleted'));
