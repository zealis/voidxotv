-- 回滚到「用户名 + 密码」的登录方式。
-- username 无法从邮箱还原成原来的值（原值已被删除），这里按邮箱本地部分回填，
-- 再统一加 a_<id> 前缀兜底重复，保证 NOT NULL 与唯一约束都能立起来。
ALTER TABLE admin_users ADD COLUMN username TEXT;
UPDATE admin_users SET username = 'a_' || id;
ALTER TABLE admin_users ALTER COLUMN username SET NOT NULL;
ALTER TABLE admin_users ADD CONSTRAINT admin_users_username_key UNIQUE (username);

DROP INDEX IF EXISTS admin_users_email_key;
ALTER TABLE admin_users DROP COLUMN email;
