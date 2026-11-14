-- 创作者认证需要可撤回：0.20.0 起后台支持「取消认证」，
-- 但 user_sanctions.action 的 CHECK 只放了 verify_creator，写入会撞 23514。
-- 这里把下行动作加进白名单（约束名沿用原建表时的自动命名，先 DROP 再 ADD）。
ALTER TABLE user_sanctions DROP CONSTRAINT user_sanctions_action_check;
ALTER TABLE user_sanctions ADD CONSTRAINT user_sanctions_action_check
    CHECK (action IN ('warn', 'mute', 'ban', 'unban', 'verify_creator', 'unverify_creator'));
