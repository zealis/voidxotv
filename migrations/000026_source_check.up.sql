-- 去中心化（external_direct）在线地址存活检测：记录连续失败次数与最近检测时间。
-- 连续失败达到阈值（3 次）会把作品暂时移出推荐流并通知作者；恢复可访问自动回来。
ALTER TABLE contents
    ADD COLUMN IF NOT EXISTS source_fail_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS source_checked_at TIMESTAMPTZ;
