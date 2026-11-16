-- 举报理由增加 dead_video（视频失效）：配合去中心化直链内容，源站下架 / 失效可被观众举报。
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_reason_check;
ALTER TABLE reports ADD CONSTRAINT reports_reason_check
    CHECK (reason = ANY (ARRAY['vulgar'::text, 'copyright'::text, 'harassment'::text, 'fraud'::text, 'other'::text, 'dead_video'::text]));
