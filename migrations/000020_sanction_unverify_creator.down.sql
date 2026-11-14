-- 回滚会丢掉「取消认证」的历史记录（旧约束不接受这个值），先清掉再收紧。
-- 注意：这等于抹掉撤回操作在处置日志里的痕迹，只在确实要退回旧版本时执行。
DELETE FROM user_sanctions WHERE action = 'unverify_creator';
ALTER TABLE user_sanctions DROP CONSTRAINT user_sanctions_action_check;
ALTER TABLE user_sanctions ADD CONSTRAINT user_sanctions_action_check
    CHECK (action IN ('warn', 'mute', 'ban', 'unban', 'verify_creator'));
