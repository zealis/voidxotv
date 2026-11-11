UPDATE contents SET publish_state = 'draft' WHERE publish_state = 'deleted';
ALTER TABLE contents DROP CONSTRAINT IF EXISTS contents_publish_state_check;
ALTER TABLE contents ADD CONSTRAINT contents_publish_state_check
    CHECK (publish_state IN ('draft', 'scheduled', 'published'));
