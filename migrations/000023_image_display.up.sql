-- 图集原图与展示图分开存：原有记录一律视为 original，新上传额外写一条 display。
ALTER TABLE storage_objects
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'original';

ALTER TABLE storage_objects
    ADD CONSTRAINT storage_objects_role_check CHECK (role IN ('original', 'display'));

CREATE INDEX IF NOT EXISTS storage_objects_media_role_idx
    ON storage_objects (media_asset_id, role);
