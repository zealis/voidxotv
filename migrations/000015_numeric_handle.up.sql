-- ── 夜街号改为纯数字（= users.id）：取消英文 handle 作为账号标识 ──
-- 背景：登录改用「邮箱 + 密码」，注册不再让用户填账号；夜街号直接取数据库自增 ID。
-- 存量英文 handle 在这里统一改成 id 的字符串形式，display_name 补成「用户 + 夜街号」。
--
-- 分两步改 handle：先落到不可能冲突的临时前缀，再写成数字，
-- 避免某用户原本的 handle 恰好是数字、与另一个用户的 id 撞上唯一索引。
UPDATE users SET handle = 'migrating-' || id WHERE handle !~ '^[0-9]+$';
UPDATE users SET handle = id::text WHERE handle = 'migrating-' || id;

-- 默认昵称：此前注册不写 display_name，前端只能回落显示 handle。
UPDATE users SET display_name = '用户' || id WHERE btrim(display_name) = '';

-- 登录只认邮箱，没有邮箱的存量账号（早期调试号）补一个占位邮箱，否则永久无法登录。
UPDATE users SET email = 'user' || id || '@example.test' WHERE email IS NULL OR btrim(email) = '';
