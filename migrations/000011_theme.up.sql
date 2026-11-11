-- ── 工具型页面主题偏好（UI 规范 §14）────────────────────────────
-- 仅作用于工具型页面（设置 / 创作者中心）；沉浸界面恒为深色，不读此列。
-- system = 跟随系统 prefers-color-scheme；用户显式 light / dark 优先。
-- 默认 system，与「默认可跟随系统，用户手动选择优先」一致。

ALTER TABLE user_settings ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'system';
ALTER TABLE user_settings DROP CONSTRAINT IF EXISTS user_settings_theme_check;
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark'));
