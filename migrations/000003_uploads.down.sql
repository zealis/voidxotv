DROP TABLE IF EXISTS artifact_manifests;
DROP INDEX IF EXISTS upload_jobs_status_idx;
ALTER TABLE upload_jobs DROP COLUMN IF EXISTS part_count;
ALTER TABLE upload_jobs DROP COLUMN IF EXISTS multipart_upload_id;
