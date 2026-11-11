-- 夜街 M3：Go 后端内 Delivery 模块的调度记账（开发技术规范 §10.4 / §10.5）
-- 页面产物不由 Delivery 持有；这里只存任务、版本标记、依赖、审计与失效日志。

CREATE TABLE delivery_pages (
    page_key           TEXT PRIMARY KEY,
    page_type          TEXT NOT NULL,
    desired_version    BIGINT NOT NULL DEFAULT 0,
    published_version  BIGINT NOT NULL DEFAULT 0,
    published_at       TIMESTAMPTZ,
    last_revalidated_at TIMESTAMPTZ,
    status             TEXT NOT NULL DEFAULT 'active',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE delivery_jobs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_key        TEXT NOT NULL,
    page_type       TEXT NOT NULL,
    trigger_type    TEXT NOT NULL DEFAULT 'content',
    priority        INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','processing','published','retrying','dead_letter','cancelled')),
    target_version  BIGINT NOT NULL DEFAULT 0,
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 5,
    scheduled_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    last_error      TEXT,
    lease_owner     TEXT,
    lease_expires_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 同一页面同一目标版本只保留一个活动任务（任务合并 / 幂等）。
CREATE UNIQUE INDEX delivery_jobs_active_version_idx
    ON delivery_jobs (page_key, target_version)
    WHERE status IN ('pending','processing','retrying');
CREATE INDEX delivery_jobs_claim_idx ON delivery_jobs (status, scheduled_at, priority DESC, created_at);

CREATE TABLE delivery_dependencies (
    page_key   TEXT NOT NULL,
    depends_on TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'content',
    PRIMARY KEY (page_key, depends_on)
);

CREATE TABLE delivery_versions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_key   TEXT NOT NULL,
    job_id     BIGINT,
    version    BIGINT NOT NULL,
    outcome    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX delivery_versions_page_idx ON delivery_versions (page_key, version DESC);

CREATE TABLE delivery_logs (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id     BIGINT,
    page_key   TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    ok         BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX delivery_logs_created_idx ON delivery_logs (created_at DESC);

-- 内容事件触发页面依赖：详情页、创作者主页、首页公共流与 sitemap。
INSERT INTO delivery_dependencies (page_key, depends_on, kind) VALUES
    ('/', 'content', 'all'),
    ('sitemap:all', 'content', 'all');
