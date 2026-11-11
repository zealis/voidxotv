-- 夜街 · 本地开发演示数据（M2 沉浸流）
-- 仅用于开发环境；禁止在生产执行。
-- M4 审核队列落地后，审核通过与 feed_eligible 应改由后台审核动作写入。
-- 可重复执行：内容用固定 slug，资产与对象按存在性判断插入。

-- 让 M1 端到端发布的视频在公开 feed 中可见（相当于人工完成审核）。
UPDATE contents
SET review_status = 'approved', feed_eligible = true
WHERE publish_state = 'published'
  AND review_status = 'pending'
  AND visibility = 'public'
  AND published_at IS NOT NULL;

-- 没有转码产物的早期草稿不应出现在公开流里（M4 会在发布/审核流程里强制校验）。
UPDATE contents
SET feed_eligible = false
WHERE type = 'video'
  AND NOT EXISTS (SELECT 1 FROM artifact_manifests m WHERE m.content_id = contents.id);

-- 两条图集条目：图片地址指向前端静态演示素材（driver=local_temp）。
INSERT INTO contents
    (slug, author_id, type, title, description, visibility,
     publish_state, review_status, published_at, feed_eligible)
SELECT 'demo-gallery-neon', u.id, 'image', '霓虹未眠',
       '雨后的街口，灯牌把水面染成粉色。#城市夜行 #街头摄影',
       'public', 'published', 'approved', now(), true
FROM users u ORDER BY u.id LIMIT 1
ON CONFLICT (slug) DO NOTHING;

INSERT INTO contents
    (slug, author_id, type, title, description, visibility,
     publish_state, review_status, published_at, feed_eligible)
SELECT 'demo-gallery-rain', u.id, 'image', '夜班列车',
       '末班车之后，整条街只剩便利店还亮着。#深夜散步 #城市光影',
       'public', 'published', 'approved', now(), true
FROM users u ORDER BY u.id LIMIT 1
ON CONFLICT (slug) DO NOTHING;

INSERT INTO media_assets (content_id, kind, position, mime_type, byte_size, processing_status)
SELECT c.id, 'image', g.n - 1, 'image/jpeg', 0, 'ready'
FROM contents c CROSS JOIN generate_series(1, 4) AS g(n)
WHERE c.slug IN ('demo-gallery-neon', 'demo-gallery-rain')
  AND NOT EXISTS (SELECT 1 FROM media_assets m WHERE m.content_id = c.id);

INSERT INTO storage_objects (media_asset_id, driver, bucket, object_key, byte_size)
SELECT m.id, 'local_temp', '',
       '/demo/c' || (m.position + CASE WHEN c.slug = 'demo-gallery-rain' THEN 5 ELSE 1 END)::text || '.jpeg',
       0
FROM media_assets m JOIN contents c ON c.id = m.content_id
WHERE c.slug IN ('demo-gallery-neon', 'demo-gallery-rain')
  AND NOT EXISTS (SELECT 1 FROM storage_objects s WHERE s.media_asset_id = m.id);
