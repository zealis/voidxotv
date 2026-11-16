ALTER TABLE contents
    DROP COLUMN IF EXISTS source_fail_count,
    DROP COLUMN IF EXISTS source_checked_at;
