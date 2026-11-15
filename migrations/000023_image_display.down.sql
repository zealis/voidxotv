DROP INDEX IF EXISTS storage_objects_media_role_idx;
ALTER TABLE storage_objects DROP CONSTRAINT IF EXISTS storage_objects_role_check;
ALTER TABLE storage_objects DROP COLUMN IF EXISTS role;
