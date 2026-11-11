-- 夜街 M1-C/D：上传票据与转码产物清单
ALTER TABLE upload_jobs
    ADD COLUMN multipart_upload_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN part_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX upload_jobs_status_idx ON upload_jobs (status, created_at);

CREATE TABLE artifact_manifests (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id          BIGINT NOT NULL UNIQUE REFERENCES transcode_jobs(id) ON DELETE CASCADE,
    content_id      BIGINT NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    profile_version TEXT NOT NULL,
    manifest        JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
