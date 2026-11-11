-- 夜街 M4：运营后台、审核/举报、互动、统计聚合与混合来源
-- 依据：开发技术规范 §4.5/§4.6/§4.7、§7.6、§9.4、§9.5、§11.5/§11.6；UI 规范 §11.5

-- ── 内容审核（§9.4）──────────────────────────────────────────────
-- 「限流」保留落地页但退出推荐流与热门榜（§9.4），故与 feed_eligible 正交。
ALTER TABLE contents ADD COLUMN recommend_eligible BOOLEAN NOT NULL DEFAULT true;

-- 每次审核动作留痕；首次决定时间用于「平均等待时长」口径（§7.6）。
CREATE TABLE content_review_decisions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    admin_id   BIGINT NOT NULL REFERENCES admin_users(id),
    action     TEXT NOT NULL
               CHECK (action IN ('approve', 'reject', 'take_down', 'limit', 'restore')),
    reason     TEXT NOT NULL DEFAULT '',
    risk_score INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX content_review_decisions_content_idx
    ON content_review_decisions (content_id, created_at DESC);
CREATE INDEX content_review_decisions_created_idx
    ON content_review_decisions (created_at DESC);

-- 机器检测与版权投诉信号；举报次数从 reports 实时聚合，不在此表重复存储。
CREATE TABLE content_risk_signals (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    signal     TEXT NOT NULL CHECK (signal IN ('machine', 'copyright', 'recheck')),
    level      INTEGER NOT NULL DEFAULT 1 CHECK (level BETWEEN 1 AND 5),
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX content_risk_signals_content_idx ON content_risk_signals (content_id);

-- ── 举报并单（§9.5）──────────────────────────────────────────────
-- 同一对象 24 小时内的多条举报合并为一个处理单元。
CREATE TABLE report_cases (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    object_type  TEXT NOT NULL CHECK (object_type IN ('content', 'comment', 'user')),
    object_id    BIGINT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'upheld', 'dismissed')),
    report_count INTEGER NOT NULL DEFAULT 1,
    resolution   TEXT NOT NULL DEFAULT '',
    linked_action TEXT NOT NULL DEFAULT '',
    resolved_by  BIGINT REFERENCES admin_users(id),
    opened_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ
);
CREATE INDEX report_cases_pending_idx ON report_cases (status, opened_at DESC);
CREATE INDEX report_cases_object_idx ON report_cases (object_type, object_id, opened_at DESC);

ALTER TABLE reports ADD COLUMN case_id BIGINT REFERENCES report_cases(id) ON DELETE SET NULL;
CREATE INDEX reports_case_idx ON reports (case_id);

-- ── 用户管理与站内通知（§4.7）────────────────────────────────────
CREATE TABLE user_sanctions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    admin_id   BIGINT NOT NULL REFERENCES admin_users(id),
    action     TEXT NOT NULL CHECK (action IN ('warn', 'mute', 'ban', 'unban', 'verify_creator')),
    reason     TEXT NOT NULL,
    notified   BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX user_sanctions_user_idx ON user_sanctions (user_id, created_at DESC);

CREATE TABLE user_notifications (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'system',
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX user_notifications_user_idx ON user_notifications (user_id, created_at DESC);

-- ── 互动持久化（§8 /api/v1/reactions、/comments）──────────────────
-- 唯一约束保证幂等（§8「互动关系通过数据库唯一约束保证幂等」）。
CREATE TABLE reactions (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('like', 'favorite')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, content_id, kind)
);
CREATE INDEX reactions_content_idx ON reactions (content_id, kind);

CREATE TABLE comments (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'visible'
               CHECK (status IN ('visible', 'removed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX comments_content_idx ON comments (content_id, created_at DESC);

-- 内容累计计数：播放由 Redis 事件计数批量落库，点赞等由唯一约束直接计数。
CREATE TABLE content_counters (
    content_id BIGINT PRIMARY KEY REFERENCES contents(id) ON DELETE CASCADE,
    plays      BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 统计聚合（§7.6）──────────────────────────────────────────────
-- 概览页只读聚合表；pending_reviews 为时点值，其余为周期增量。
CREATE TABLE stats_overview_5m (
    bucket            TIMESTAMPTZ PRIMARY KEY,
    plays             BIGINT NOT NULL DEFAULT 0,
    new_users         BIGINT NOT NULL DEFAULT 0,
    new_contents      BIGINT NOT NULL DEFAULT 0,
    new_reports       BIGINT NOT NULL DEFAULT 0,
    resolved_reports  BIGINT NOT NULL DEFAULT 0,
    pending_reviews   BIGINT NOT NULL DEFAULT 0,
    total_plays       BIGINT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE stats_daily (
    day              DATE PRIMARY KEY,
    plays            BIGINT NOT NULL DEFAULT 0,
    new_users        BIGINT NOT NULL DEFAULT 0,
    new_contents     BIGINT NOT NULL DEFAULT 0,
    new_reports      BIGINT NOT NULL DEFAULT 0,
    resolved_reports BIGINT NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 热门 TOP：近 7 日播放 + 点赞加权，每小时重算；只含公开且已上架内容。
CREATE TABLE hot_contents (
    content_id  BIGINT PRIMARY KEY REFERENCES contents(id) ON DELETE CASCADE,
    score       NUMERIC(14, 2) NOT NULL DEFAULT 0,
    plays       BIGINT NOT NULL DEFAULT 0,
    likes       BIGINT NOT NULL DEFAULT 0,
    rank_no     INTEGER NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 混合来源（§4.5 / §4.6）───────────────────────────────────────
-- 密钥只存 secret_ref（服务端密钥配置引用），前端永远只看到脱敏状态。
CREATE TABLE source_connectors (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug              TEXT NOT NULL UNIQUE,
    name              TEXT NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('third_party_api', 'webdav')),
    base_url          TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'unauthorized'
                      CHECK (status IN ('active', 'disabled', 'unauthorized', 'error')),
    config            JSONB NOT NULL DEFAULT '{}'::jsonb,
    secret_ref        TEXT NOT NULL DEFAULT '',
    allow_direct_play BOOLEAN NOT NULL DEFAULT false,
    allow_import      BOOLEAN NOT NULL DEFAULT false,
    last_refresh_at   TIMESTAMPTZ,
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 导入任务是事实源；媒体字节由 Worker 下载后走既有转码链路，不经应用进程代理。
CREATE TABLE import_jobs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    connector_id    BIGINT NOT NULL REFERENCES source_connectors(id),
    external_id     TEXT NOT NULL DEFAULT '',
    source_url      TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'downloading', 'transcoding', 'published', 'failed')),
    content_id      BIGINT REFERENCES contents(id) ON DELETE SET NULL,
    byte_size       BIGINT NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX import_jobs_status_idx ON import_jobs (status, created_at DESC);

-- 外部播放地址刷新记录（§4.5 第 4/5 条）。
CREATE TABLE external_playback_sources (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_id       BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    connector_id     BIGINT NOT NULL REFERENCES source_connectors(id),
    external_id      TEXT NOT NULL,
    address          TEXT NOT NULL DEFAULT '',
    expires_at       TIMESTAMPTZ,
    playable         BOOLEAN NOT NULL DEFAULT true,
    last_refreshed_at TIMESTAMPTZ,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connector_id, external_id)
);

-- ── 话题运营位（§4.7）────────────────────────────────────────────
CREATE TABLE topic_placements (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic_id     BIGINT NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    placement    TEXT NOT NULL CHECK (placement IN ('featured', 'pinned')),
    effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   BIGINT NOT NULL REFERENCES admin_users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (topic_id, placement)
);

CREATE TRIGGER source_connectors_set_updated_at BEFORE UPDATE ON source_connectors
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER import_jobs_set_updated_at BEFORE UPDATE ON import_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
