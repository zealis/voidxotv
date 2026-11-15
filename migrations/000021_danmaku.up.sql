-- ── 弹幕（按视频时间轴持久化）──────────────────────────────────
-- time_ms 记录发送时的视频播放位置；前端播放到该点时触发一次滚动，
-- 视频 loop 回绕后重新播放，刷新页面从接口重新拉取，不再依赖组件内存。

CREATE TABLE IF NOT EXISTS danmaku (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id  BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body        TEXT NOT NULL,
    time_ms     INTEGER NOT NULL DEFAULT 0 CHECK (time_ms >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 拉取按时间点顺序，索引同时覆盖 content_id 过滤与排序。
CREATE INDEX IF NOT EXISTS danmaku_content_time_idx ON danmaku (content_id, time_ms, id);
