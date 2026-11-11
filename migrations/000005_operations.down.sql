-- M4 回滚：按依赖倒序移除，全部使用 IF EXISTS 便于本地重建。
DROP TRIGGER IF EXISTS import_jobs_set_updated_at ON import_jobs;
DROP TRIGGER IF EXISTS source_connectors_set_updated_at ON source_connectors;

DROP TABLE IF EXISTS topic_placements;
DROP TABLE IF EXISTS external_playback_sources;
DROP TABLE IF EXISTS import_jobs;
DROP TABLE IF EXISTS source_connectors;

DROP TABLE IF EXISTS hot_contents;
DROP TABLE IF EXISTS stats_daily;
DROP TABLE IF EXISTS stats_overview_5m;
DROP TABLE IF EXISTS content_counters;

DROP TABLE IF EXISTS comments;
DROP TABLE IF EXISTS reactions;

DROP TABLE IF EXISTS user_notifications;
DROP TABLE IF EXISTS user_sanctions;

ALTER TABLE reports DROP COLUMN IF EXISTS case_id;
DROP TABLE IF EXISTS report_cases;

DROP TABLE IF EXISTS content_risk_signals;
DROP TABLE IF EXISTS content_review_decisions;

ALTER TABLE contents DROP COLUMN IF EXISTS recommend_eligible;
