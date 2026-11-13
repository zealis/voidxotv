-- 不可逆：英文 handle 已被数字夜街号覆盖，原值没有留档，无法自动还原。
-- 只回收本迁移可能留下的占位形态，避免回滚后卡在中间状态。
UPDATE users SET handle = 'user_' || id WHERE handle = id::text;
