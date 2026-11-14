-- GIF 图源增加 KLIPY（浏览器直连）：补它专用的 5 个键。
-- 与 apihz 的差别是「密钥要不要保密」：KLIPY 的 app_key 按设计就出现在浏览器请求里，
-- 所以它由 /api/v1/gifs/config 下发给前端；apihz 的 id/key 永远只留在服务端。
INSERT INTO system_settings (key, value) VALUES
    ('gif.klipy_key', ''),
    ('gif.klipy_filter', 'off'),
    ('gif.klipy_locale', 'cn'),
    ('gif.klipy_limit', '24'),
    ('gif.klipy_cache_days', '1')
ON CONFLICT (key) DO NOTHING;
