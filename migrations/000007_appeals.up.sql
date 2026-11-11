-- ── 内容申诉（§9.4：驳回/下架须通知作者并允许申诉）─────────────────
-- 一条申诉对应一次审核决定；同一作品同时只允许一个待处理申诉，
-- 避免作者反复提交制造队列噪声。

CREATE TABLE IF NOT EXISTS content_appeals (
    id            BIGSERIAL PRIMARY KEY,
    content_id    BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    author_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- 申诉针对的决定；决定被清理时保留申诉本身。
    decision_id   BIGINT REFERENCES content_review_decisions(id) ON DELETE SET NULL,
    reason        TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'accepted', 'rejected')),
    reviewer_id   BIGINT REFERENCES admin_users(id) ON DELETE SET NULL,
    decision_note TEXT NOT NULL DEFAULT '',
    reviewed_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS content_appeals_open_idx
    ON content_appeals (content_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS content_appeals_queue_idx
    ON content_appeals (status, created_at);
CREATE INDEX IF NOT EXISTS content_appeals_author_idx
    ON content_appeals (author_id, created_at DESC);

-- ── 通知读取（通知此前只写不读，作者无从查看审核结果）────────────────
CREATE INDEX IF NOT EXISTS user_notifications_user_idx
    ON user_notifications (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS user_notifications_unread_idx
    ON user_notifications (user_id, created_at DESC) WHERE read_at IS NULL;
