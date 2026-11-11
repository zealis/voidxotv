-- 夜街 M1：好友关系、话题、举报单（开发技术规范 §3.1「新增实体与关系」）
CREATE TABLE follows (
    follower_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX follows_followee_idx ON follows (followee_id, created_at DESC);

CREATE TABLE topics (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'available', 'offline')),
    creator_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE content_topics (
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    topic_id   BIGINT NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    PRIMARY KEY (content_id, topic_id)
);

CREATE TABLE reports (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reporter_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    object_type    TEXT NOT NULL CHECK (object_type IN ('content', 'comment', 'user')),
    object_id      BIGINT NOT NULL,
    reason         TEXT NOT NULL CHECK (reason IN ('vulgar', 'copyright', 'harassment', 'fraud', 'other')),
    detail         TEXT NOT NULL DEFAULT '',
    evidence       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'upheld', 'dismissed', 'duplicate')),
    merged_at      TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at    TIMESTAMPTZ
);
CREATE INDEX reports_pending_idx ON reports (status, created_at DESC);

CREATE TRIGGER topics_set_updated_at BEFORE UPDATE ON topics
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
