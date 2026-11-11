-- 夜街 M0：账号、后台审计、统一内容模型、媒体资产与任务事实源
-- PostgreSQL 17。所有迁移成对提供 up/down，并由 CI 连续执行两次验证幂等。

CREATE TABLE users (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    handle       TEXT NOT NULL UNIQUE,
    email        TEXT UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    avatar_key   TEXT,
    role         TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'creator', 'admin')),
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'muted', 'banned')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 长期会话记录只存令牌哈希；活跃会话在 Redis（开发技术规范 §11.4）。
CREATE TABLE user_sessions (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash     BYTEA NOT NULL UNIQUE,
    device_id      TEXT,
    ip_address     INET,
    user_agent     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL,
    revoked_at     TIMESTAMPTZ
);
CREATE INDEX user_sessions_user_active_idx ON user_sessions (user_id, revoked_at, expires_at);

-- 后台账号与普通用户体系分离，四档 RBAC（开发技术规范 §11.6）。
CREATE TABLE admin_users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('super_admin', 'reviewer', 'operator', 'readonly')),
    totp_secret   TEXT,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 后台全部写操作写审计，日志只读、保留 ≥ 1 年。
CREATE TABLE admin_audit_logs (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    admin_id       BIGINT NOT NULL REFERENCES admin_users(id),
    role           TEXT NOT NULL,
    action         TEXT NOT NULL,
    object_type    TEXT NOT NULL,
    object_id      TEXT NOT NULL DEFAULT '',
    before_summary JSONB,
    after_summary  JSONB,
    ip_address     INET,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_logs_created_idx ON admin_audit_logs (created_at DESC);

-- 统一内容模型：只做 video / image；发布状态与审核状态正交；好友可见性按 follows 双向判定。
CREATE TABLE contents (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug           TEXT NOT NULL UNIQUE,
    author_id      BIGINT NOT NULL REFERENCES users(id),
    type           TEXT NOT NULL CHECK (type IN ('video', 'image')),
    title          TEXT NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    origin         TEXT NOT NULL DEFAULT 'uploaded'
                   CHECK (origin IN ('uploaded', 'external_direct', 'external_imported')),
    visibility     TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'friends', 'private')),
    publish_state  TEXT NOT NULL DEFAULT 'draft' CHECK (publish_state IN ('draft', 'scheduled', 'published')),
    review_status  TEXT NOT NULL DEFAULT 'pending'
                   CHECK (review_status IN ('pending', 'approved', 'rejected', 'taken_down', 'limited')),
    scheduled_at   TIMESTAMPTZ,
    published_at   TIMESTAMPTZ,
    poi            JSONB,
    duration_ms    BIGINT,
    feed_eligible  BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX contents_author_idx ON contents (author_id, created_at DESC);
-- 部分索引只覆盖真正可进入公开 feed 的行（开发技术规范 §3.3）。
CREATE INDEX contents_public_feed_idx ON contents (published_at DESC, id DESC)
    WHERE publish_state = 'published' AND review_status = 'approved'
      AND visibility = 'public' AND feed_eligible;

-- 媒体资产只记录存储位置与元数据，不把永久 URL 拼死在内容字段中。
CREATE TABLE media_assets (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id        BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('source', 'mezzanine', 'hls', 'mp4', 'image', 'cover', 'subtitle', 'storyboard')),
    position          INTEGER NOT NULL DEFAULT 0,
    mime_type         TEXT NOT NULL DEFAULT '',
    byte_size         BIGINT NOT NULL DEFAULT 0,
    duration_ms       BIGINT,
    width             INTEGER,
    height            INTEGER,
    codec             TEXT,
    processing_status TEXT NOT NULL DEFAULT 'pending' CHECK (processing_status IN ('pending', 'processing', 'ready', 'failed')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX media_assets_content_idx ON media_assets (content_id, position);

CREATE TABLE storage_objects (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    media_asset_id BIGINT REFERENCES media_assets(id) ON DELETE CASCADE,
    driver         TEXT NOT NULL CHECK (driver IN ('s3', 'webdav', 'external', 'local_temp')),
    bucket         TEXT NOT NULL DEFAULT '',
    object_key     TEXT NOT NULL,
    checksum       TEXT NOT NULL DEFAULT '',
    byte_size      BIGINT NOT NULL DEFAULT 0,
    retention_until TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (driver, bucket, object_key)
);

CREATE TABLE playback_sources (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id  BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    address_type TEXT NOT NULL CHECK (address_type IN ('stable', 'signed')),
    quality     TEXT NOT NULL DEFAULT '',
    language    TEXT NOT NULL DEFAULT '',
    object_key  TEXT NOT NULL DEFAULT '',
    priority    INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX playback_sources_content_idx ON playback_sources (content_id, priority);

-- 上传与转码任务：PostgreSQL 任务表是唯一事实源，Redis 只负责唤醒。
CREATE TABLE upload_jobs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id      BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    owner_id        BIGINT NOT NULL REFERENCES users(id),
    idempotency_key TEXT NOT NULL,
    object_key      TEXT NOT NULL,
    mime_type       TEXT NOT NULL DEFAULT '',
    byte_size       BIGINT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'uploaded',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, idempotency_key)
);

CREATE TABLE transcode_jobs (
    id                     BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    upload_id              BIGINT NOT NULL UNIQUE REFERENCES upload_jobs(id) ON DELETE CASCADE,
    content_id             BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    status                 TEXT NOT NULL DEFAULT 'uploaded'
                           CHECK (status IN ('uploaded', 'probing', 'processing', 'packaging', 'verifying', 'published', 'transcode_failed')),
    attempt_count          INTEGER NOT NULL DEFAULT 0,
    max_attempts           INTEGER NOT NULL DEFAULT 4,
    next_attempt_at        TIMESTAMPTZ,
    lease_owner            TEXT,
    lease_expires_at       TIMESTAMPTZ,
    last_error             TEXT,
    profile_version        TEXT NOT NULL DEFAULT 'h264-aac-v1',
    transcode_succeeded_at TIMESTAMPTZ,
    source_expires_at      TIMESTAMPTZ,
    mezzanine_expires_at   TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX transcode_jobs_claim_idx ON transcode_jobs (status, next_attempt_at, lease_expires_at);

-- Transactional Outbox：业务写入与事件在同一事务提交，消费者必须幂等。
CREATE TABLE outbox_events (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_key    TEXT NOT NULL UNIQUE,
    event_type   TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);
CREATE INDEX outbox_events_claim_idx ON outbox_events (status, next_attempt_at, created_at);

CREATE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER admin_users_set_updated_at BEFORE UPDATE ON admin_users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER contents_set_updated_at BEFORE UPDATE ON contents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER upload_jobs_set_updated_at BEFORE UPDATE ON upload_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER transcode_jobs_set_updated_at BEFORE UPDATE ON transcode_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
