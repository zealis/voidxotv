-- ── 观看进度 / 稍后再看 / 系统设置（§3.1 数据模型 / §4.1 /library / §7.6）──
-- watch_progress：播放位置持久化，支撑「历史」与续播；user+content 唯一，upsert 更新。
CREATE TABLE IF NOT EXISTS watch_progress (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content_id  BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    position_ms BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, content_id)
);
CREATE INDEX IF NOT EXISTS watch_progress_user_idx
    ON watch_progress (user_id, updated_at DESC);

-- watchlist：稍后再看；收藏沿用 reactions(kind='favorite')，二者互不替代。
CREATE TABLE IF NOT EXISTS watchlist (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, content_id)
);

-- system_settings：运行时可调的系统级参数（§7.6 热门榜权重不在代码里写死）。
CREATE TABLE IF NOT EXISTS system_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT
);
INSERT INTO system_settings (key, value) VALUES
    ('hot.play_weight', '1'),
    ('hot.like_weight', '3')
ON CONFLICT (key) DO NOTHING;
