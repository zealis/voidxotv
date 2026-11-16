-- 去中心化（external_direct）：视频不落存储，contents 直接收外部直链。
-- origin 列在 000001 就预留了 external_direct / external_imported 取值，这里补上承载链接的列。
ALTER TABLE contents
    ADD COLUMN IF NOT EXISTS source_url TEXT NOT NULL DEFAULT '';
