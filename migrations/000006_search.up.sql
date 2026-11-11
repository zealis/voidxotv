-- ── 分类与标签（§3.1 核心实体；§9.2 搜索字段来源）─────────────────
-- 分类用于搜索筛选与 /category/[slug] 落地页；标签用于检索命中的第三类字段。
-- 二者都不参与沉浸流排序，只做归集与筛选。

CREATE TABLE IF NOT EXISTS categories (
    id          BIGSERIAL PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    position    INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'available'
                CHECK (status IN ('available', 'hidden')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS content_categories (
    content_id  BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    category_id BIGINT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (content_id, category_id)
);

-- 分类详情页按「该分类下内容倒序」查询，索引与查询方向一致。
CREATE INDEX IF NOT EXISTS content_categories_category_idx
    ON content_categories (category_id, content_id DESC);

CREATE TABLE IF NOT EXISTS tags (
    id         BIGSERIAL PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name)
);

CREATE TABLE IF NOT EXISTS content_tags (
    content_id BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    tag_id     BIGINT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (content_id, tag_id)
);

CREATE INDEX IF NOT EXISTS content_tags_tag_idx
    ON content_tags (tag_id, content_id DESC);

-- ── 检索索引（§9.2「可按需启用 pg_trgm 提升子串匹配性能」）─────────
-- ILIKE '%词%' 无法使用 B-Tree；trigram GIN 索引对 ≥3 字符的子串可用，
-- 2 字符查询回退顺序扫描（最小查询长度即 2 个字符）。
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS contents_title_trgm_idx
    ON contents USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS contents_description_trgm_idx
    ON contents USING gin (description gin_trgm_ops);
CREATE INDEX IF NOT EXISTS tags_name_trgm_idx
    ON tags USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS topics_name_trgm_idx
    ON topics USING gin (name gin_trgm_ops);

-- 顺序扫描时的可见性过滤也需要索引支撑：沿用公开 feed 的部分索引条件，
-- 额外放宽 recommend_eligible（限流内容仍可检索到落地页）。

-- ── 搜索统计（§9.2 热门搜索、搜索错误与耗时统计）──────────────────
-- 一行 = 一次成功执行的搜索；normalized 为标准化后的查询词，用于聚合热门。
CREATE TABLE IF NOT EXISTS search_queries (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    query        TEXT NOT NULL,
    normalized   TEXT NOT NULL,
    result_count INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    filters      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS search_queries_normalized_idx
    ON search_queries (normalized, created_at DESC);
CREATE INDEX IF NOT EXISTS search_queries_created_idx
    ON search_queries (created_at DESC);

-- 基线分类：作为平台参考数据随迁移下发，运营可在后台调整顺序与显隐。
INSERT INTO categories (slug, name, description, position) VALUES
    ('night-city', '夜景', '城市夜色、霓虹与街灯', 10),
    ('live-music', '音乐现场', 'Live、乐队与即兴演出', 20),
    ('street',     '街头', '街头纪实与即兴瞬间', 30),
    ('travel',     '旅行', '路上的风景与目的地', 40),
    ('daily',      '日常', '生活切片与城市日常', 50)
ON CONFLICT (slug) DO NOTHING;
