-- ── 后台账号改为「邮箱 + 密码」：取消用户名登录，与前台账号同一口径 ──
-- 背景：前台 0.14.0 起已只认邮箱；后台仍用 username，登录框和建号表单都多一个「账号」字段，
-- 运维视角没有必要。这里把 username 换成 email，登录 / 建号 / 审计 / 列表统一用邮箱。

ALTER TABLE admin_users ADD COLUMN email TEXT;

-- 存量账号（seed 出来的 yj_super_admin 等）没有邮箱：不补一个可登录的标识，
-- 账号会在丢掉 username 之后永久锁死。@admin.local 是明显的占位域，不是真实收件地址。
UPDATE admin_users SET email = lower(username) || '@admin.local'
 WHERE email IS NULL OR btrim(email) = '';

ALTER TABLE admin_users ALTER COLUMN email SET NOT NULL;

-- 与前台一致：写入前统一小写，唯一索引也建在 lower(email) 上，
-- 避免 Admin@x.com 与 admin@x.com 各占一行。
CREATE UNIQUE INDEX admin_users_email_key ON admin_users (lower(email));

-- 用户名不再是登录凭据，也不再对外展示：邮箱本身就是运维视角的标识。
ALTER TABLE admin_users DROP COLUMN username;
