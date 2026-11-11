DROP INDEX IF EXISTS topics_name_trgm_idx;
DROP INDEX IF EXISTS tags_name_trgm_idx;
DROP INDEX IF EXISTS contents_description_trgm_idx;
DROP INDEX IF EXISTS contents_title_trgm_idx;

DROP TABLE IF EXISTS search_queries;
DROP TABLE IF EXISTS content_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS content_categories;
DROP TABLE IF EXISTS categories;

-- pg_trgm 可能被其他功能复用，仅在无依赖对象时移除；失败不阻塞回滚。
DROP EXTENSION IF EXISTS pg_trgm;
