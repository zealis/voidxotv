-- ── 每个作品的每日播放（创作者中心数据看板）──────────────────────
-- 只存播放：点赞 / 评论在 reactions / comments 里已有时间戳，可直接按日聚合。
-- 聚合器每次折叠 Redis 累计计数时，把「本次增量」记到当天。

CREATE TABLE IF NOT EXISTS content_stats_daily (
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    day        DATE NOT NULL,
    plays      BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (content_id, day)
);

CREATE INDEX IF NOT EXISTS content_stats_daily_day_idx
    ON content_stats_daily (day);
