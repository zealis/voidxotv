-- GIF 表情包（评论 GIF）：图源开关 + 搜索结果缓存。
--
-- 为什么缓存落库而不是内存：图源按天限额，热门与搜索吃的是同一份额度，
-- 同一关键词 + 同一页在缓存期内不重复请求，直接省掉绝大部分配额。
CREATE TABLE IF NOT EXISTS gif_search_cache (
    k          TEXT PRIMARY KEY,
    payload    JSONB NOT NULL,
    expire_at  TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gif_search_cache_expire_idx ON gif_search_cache (expire_at);

-- 默认关闭：没配密钥时前端应当明确提示「未配置」，而不是默默空列表。
INSERT INTO system_settings (key, value) VALUES
    ('gif.provider', 'off'),
    ('gif.apihz_line', 'cn'),
    ('gif.apihz_id', ''),
    ('gif.apihz_key', ''),
    ('gif.apihz_limit', '20'),
    ('gif.cache_days', '7')
ON CONFLICT (key) DO NOTHING;
