DROP TRIGGER IF EXISTS transcode_jobs_set_updated_at ON transcode_jobs;
DROP TRIGGER IF EXISTS upload_jobs_set_updated_at ON upload_jobs;
DROP TRIGGER IF EXISTS contents_set_updated_at ON contents;
DROP TRIGGER IF EXISTS admin_users_set_updated_at ON admin_users;
DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();

DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS transcode_jobs;
DROP TABLE IF EXISTS upload_jobs;
DROP TABLE IF EXISTS playback_sources;
DROP TABLE IF EXISTS storage_objects;
DROP TABLE IF EXISTS media_assets;
DROP TABLE IF EXISTS contents;
DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS user_sessions;
DROP TABLE IF EXISTS users;
