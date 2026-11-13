-- ── 夜街号补零到 6 位：1 → 000001，第 1000000 个用户起保持 7 位 ──
-- handle 与「默认昵称」一起改；用户自己改过的昵称（如「我是重置」）不动。
--
-- 分两步改 handle：先落到不可能冲突的临时前缀，再写成补零形式，
-- 避免某用户的 handle 恰好已是目标值、与另一个正在改写的行撞唯一索引。
UPDATE users SET handle = 'pad-' || id
 WHERE handle IS DISTINCT FROM lpad(id::text, 6, '0');

UPDATE users SET handle = lpad(id::text, 6, '0')
 WHERE handle = 'pad-' || id;

UPDATE users SET display_name = '用户' || lpad(id::text, 6, '0')
 WHERE display_name = '用户' || id::text;
