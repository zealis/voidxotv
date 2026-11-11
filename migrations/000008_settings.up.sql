-- ── 用户设置（UI 规范 §11.2：设置页按 账号 / 隐私 / 通知 / 播放与材质 / 通用 分组）──
-- 一行一用户；缺行即默认值，新用户不必预插，读到空行按默认处理。

ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS user_settings (
    user_id            BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- 通知（§9.1 三个分组）：互动（赞 / 收藏 / 评论 / @）、关注、系统（审核 / 申诉 / 举报 / 账号）
    notify_interaction BOOLEAN NOT NULL DEFAULT TRUE,
    notify_follow      BOOLEAN NOT NULL DEFAULT TRUE,
    notify_system      BOOLEAN NOT NULL DEFAULT TRUE,
    -- 免打扰时段用「当天分钟偏移」存，start = end 表示关闭。
    -- 只压制互动与关注；审核 / 申诉 / 举报结果始终送达（§9.4 要求作者能看到处置结论）。
    quiet_start        SMALLINT NOT NULL DEFAULT 0 CHECK (quiet_start BETWEEN 0 AND 1439),
    quiet_end          SMALLINT NOT NULL DEFAULT 0 CHECK (quiet_end BETWEEN 0 AND 1439),
    -- 隐私：新作品的默认可见性，发布页读取后作为初值。
    default_visibility TEXT NOT NULL DEFAULT 'public'
                       CHECK (default_visibility IN ('public', 'friends', 'private')),
    -- 播放与材质 / 通用
    autoplay           BOOLEAN NOT NULL DEFAULT TRUE,
    reduce_motion      BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 通知去重：同一对象 + 同一动作在窗口内只提醒一次，
-- 避免「反复取消点赞再点赞」把作者的通知列表刷满。
ALTER TABLE user_notifications ADD COLUMN IF NOT EXISTS dedupe_key TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS user_notifications_dedupe_idx
    ON user_notifications (user_id, dedupe_key, created_at DESC)
    WHERE dedupe_key <> '';
