package content

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const contentColumns = `
	id, slug, author_id, type, title, description, visibility, publish_state, review_status,
	COALESCE(scheduled_at, 'epoch'::timestamptz), COALESCE(published_at, 'epoch'::timestamptz),
	COALESCE(poi::text, '{}'), COALESCE(duration_ms, 0), feed_eligible, created_at, updated_at`

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// NewSlug generates an opaque, URL-safe identifier for content deep links.
func NewSlug() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (r *Repository) Create(ctx context.Context, authorID int64, input CreateDraftInput) (Content, error) {
	slug, err := NewSlug()
	if err != nil {
		return Content{}, err
	}
	const query = `INSERT INTO contents (slug, author_id, type, title, description)
		VALUES ($1, $2, $3, $4, $5) RETURNING ` + contentColumns
	return r.scan(r.db.QueryRow(ctx, query, slug, authorID, input.Type, input.Title, input.Description))
}

func (r *Repository) UpdateDraft(ctx context.Context, contentID, authorID int64, input UpdateDraftInput) (Content, error) {
	const query = `UPDATE contents SET title = $3, description = $4, visibility = $5, poi = $6::jsonb,
		scheduled_at = $7
		WHERE id = $1 AND author_id = $2 AND publish_state = 'draft' RETURNING ` + contentColumns
	content, err := r.scan(r.db.QueryRow(ctx, query, contentID, authorID,
		input.Title, input.Description, input.Visibility, jsonText(input.POI), input.ScheduledAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return Content{}, ErrNotFound
	}
	return content, err
}

// SetPublish changes only publish_state; review status remains orthogonal (开发技术规范 §6.1).
func (r *Repository) SetPublish(ctx context.Context, contentID, authorID int64, input PublishInput) (Content, error) {
	state, publishedAt := "published", any(time.Now().UTC())
	if input.ScheduledAt != nil {
		state, publishedAt = "scheduled", nil
	}
	const query = `UPDATE contents
		SET publish_state = $3, visibility = $4, poi = $5::jsonb,
		    scheduled_at = $6, published_at = $7
		WHERE id = $1 AND author_id = $2 AND publish_state IN ('draft', 'scheduled')
		RETURNING ` + contentColumns
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Content{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	content, err := r.scan(tx.QueryRow(ctx, query, contentID, authorID, state,
		input.Visibility, jsonText(input.POI), input.ScheduledAt, publishedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return Content{}, ErrNotFound
	}
	if err != nil {
		return Content{}, err
	}
	if state == "published" {
		// Publishing and its Outbox event must commit atomically (开发技术规范 §7.5).
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			VALUES ($1, 'content.published',
			  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text))
			ON CONFLICT (event_key) DO NOTHING`,
			fmt.Sprintf("content.published:%d:%d", contentID, time.Now().UnixNano()),
			contentID, content.Slug); err != nil {
			return Content{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Content{}, err
	}
	return content, nil
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Content, error) {
	return r.scan(r.db.QueryRow(ctx, `SELECT `+contentColumns+` FROM contents WHERE id = $1`, id))
}

func (r *Repository) GetBySlug(ctx context.Context, slug string) (Content, error) {
	return r.scan(r.db.QueryRow(ctx, `SELECT `+contentColumns+` FROM contents WHERE slug = $1`, slug))
}

// state 为空时返回全部未删除作品；传 draft/published/scheduled 时只返回该状态，
// 避免草稿箱在 limit 下被已发布作品挤掉（技术规范 §6.1）。
// offset + 多取一条判断 has_more，供草稿箱分页（UI 规范 §10 / §11.1 列表型页面）。
func (r *Repository) ListByAuthor(ctx context.Context, authorID int64, limit, offset int,
	state, order string) ([]Content, bool, error) {
	direction := "DESC"
	if order == "asc" {
		direction = "ASC"
	}
	// direction 来自白名单（asc/desc），不能拼接进参数位。
	rows, err := r.db.Query(ctx,
		`SELECT `+contentColumns+` FROM contents
		 WHERE author_id = $1 AND publish_state <> 'deleted'
		   AND ($4 = '' OR publish_state = $4)
		 ORDER BY created_at `+direction+`, id `+direction+` LIMIT $2 OFFSET $3`,
		authorID, limit+1, offset, state)
	if err != nil {
		return nil, false, fmt.Errorf("list contents: %w", err)
	}
	defer rows.Close()
	// 初始化成空切片而不是 nil：越界分页要返回 []，不能返回 null。
	result := make([]Content, 0, limit)
	for rows.Next() {
		content, err := r.scan(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, content)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

// TranscodeStatuses 返回每个视频内容最新一条转码任务的状态：
// ready / failed 原样返回；进行中的统一归为 processing（§10 状态徽标）。
func (r *Repository) TranscodeStatuses(ctx context.Context, contentIDs []int64) (map[int64]string, error) {
	result := make(map[int64]string, len(contentIDs))
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (content_id) content_id, status
		FROM transcode_jobs
		WHERE content_id = ANY($1)
		ORDER BY content_id, id DESC`, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("transcode statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, fmt.Errorf("scan transcode status: %w", err)
		}
		switch status {
		case "published":
			result[id] = "ready"
		case "failed", "transcode_failed":
			result[id] = "failed"
		default:
			result[id] = "processing"
		}
	}
	return result, rows.Err()
}

// AreMutualFollows implements "好友 = 互相关注" without a friends table (开发技术规范 §3.1).
func (r *Repository) AreMutualFollows(ctx context.Context, a, b int64) (bool, error) {
	const query = `SELECT
		EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2) AND
		EXISTS (SELECT 1 FROM follows WHERE follower_id = $2 AND followee_id = $1)`
	var mutual bool
	if err := r.db.QueryRow(ctx, query, a, b).Scan(&mutual); err != nil {
		return false, fmt.Errorf("mutual follows: %w", err)
	}
	return mutual, nil
}

func (r *Repository) scan(row pgx.Row) (Content, error) {
	var content Content
	var scheduledAt, publishedAt time.Time
	var poiText string
	var durationMS int64
	if err := row.Scan(
		&content.ID, &content.Slug, &content.AuthorID, &content.Type, &content.Title, &content.Description,
		&content.Visibility, &content.PublishState, &content.ReviewStatus,
		&scheduledAt, &publishedAt, &poiText, &durationMS, &content.FeedEligible,
		&content.CreatedAt, &content.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Content{}, ErrNotFound
		}
		return Content{}, fmt.Errorf("scan content: %w", err)
	}
	if !scheduledAt.IsZero() && scheduledAt.Year() > 1970 {
		value := scheduledAt
		content.ScheduledAt = &value
	}
	if !publishedAt.IsZero() && publishedAt.Year() > 1970 {
		value := publishedAt
		content.PublishedAt = &value
	}
	content.POI = json.RawMessage(poiText)
	return content, nil
}

func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// ── 「我」页面的计数与分区（UI 规范 §10）──────────────────────────

// UserCounts 是「我」与作者主页共用的计数区口径：
// 获赞只统计公开且已通过的作品，关注/粉丝直接读 follows。
type UserCounts struct {
	Works     int64 `json:"works"`
	Likes     int64 `json:"likes_received"`
	Following int64 `json:"following"`
	Followers int64 `json:"followers"`
}

func (r *Repository) UserCounts(ctx context.Context, userID int64) (UserCounts, error) {
	var counts UserCounts
	err := r.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM contents c
		   WHERE c.author_id = $1 AND c.publish_state <> 'deleted'),
		  (SELECT count(*) FROM reactions rc JOIN contents c ON c.id = rc.content_id
		   WHERE c.author_id = $1 AND rc.kind = 'like'
		     AND c.publish_state = 'published' AND c.review_status = 'approved'
		     AND c.visibility = 'public'),
		  -- 计数与 /me/relations 列表同口径：停用 / 封禁账号不计入，
		  -- 否则「关注 3」点进去只有 2 个人。
		  (SELECT count(*) FROM follows f JOIN users u ON u.id = f.followee_id
		   WHERE f.follower_id = $1 AND u.status = 'active'),
		  (SELECT count(*) FROM follows f JOIN users u ON u.id = f.follower_id
		   WHERE f.followee_id = $1 AND u.status = 'active')`, userID).Scan(
		&counts.Works, &counts.Likes, &counts.Following, &counts.Followers)
	if err != nil {
		return counts, fmt.Errorf("user counts: %w", err)
	}
	return counts, nil
}

// ReactedContents 返回我点过赞 / 收藏过的作品，按互动时间倒序。
// 只暴露仍可访问的作品：下架、未通过、私密（本人作品除外）一律不出现。
func (r *Repository) ReactedContents(ctx context.Context, userID int64, kind string, limit int) ([]Content, error) {
	rows, err := r.db.Query(ctx, `SELECT `+contentColumns+`
		FROM contents
		JOIN (
		  SELECT content_id, max(created_at) AS reacted_at
		  FROM reactions WHERE user_id = $1 AND kind = $2 GROUP BY content_id
		) r ON r.content_id = contents.id
		WHERE contents.publish_state = 'published'
		  AND contents.review_status = 'approved'
		  AND (contents.visibility = 'public' OR contents.author_id = $1)
		ORDER BY r.reacted_at DESC, contents.id DESC
		LIMIT $3`, userID, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("reacted contents: %w", err)
	}
	defer rows.Close()
	var result []Content
	for rows.Next() {
		content, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, content)
	}
	return result, rows.Err()
}

// CardMeta 是「我」页卡片网格需要的批量元数据：封面地址（driver:key）与点赞数。
// 与 interactions.attachPosters 同口径：图集取首图、视频取产物清单里的 poster。
type CardMeta struct {
	Cover string
	Likes int64
}

func (r *Repository) CardMeta(ctx context.Context, ids []int64) (map[int64]CardMeta, error) {
	result := make(map[int64]CardMeta, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT c.id,
		       COALESCE(
		         (SELECT s.driver || ':' || s.object_key
		            FROM media_assets m
		            JOIN storage_objects s ON s.media_asset_id = m.id
		           WHERE m.content_id = c.id AND m.kind = 'image'
		           ORDER BY m.position, m.id, CASE s.role WHEN 'display' THEN 0 ELSE 1 END
		           LIMIT 1),
		         (SELECT 's3:' || (f->>'key')
		            FROM artifact_manifests am,
		                 jsonb_array_elements(am.manifest->'files') f
		           WHERE am.content_id = c.id
		             AND (f->>'key' LIKE '%/poster.webp'
		                  OR f->>'key' LIKE '%/poster-%'
		                  OR f->>'key' LIKE '%/cover-%')
		           ORDER BY CASE
		             WHEN f->>'key' LIKE '%/poster.webp' THEN 0
		             WHEN f->>'key' LIKE '%/poster-%' THEN 1
		             ELSE 2 END
		           LIMIT 1)
		       ) AS cover,
		       (SELECT count(*) FROM reactions
		         WHERE content_id = c.id AND kind = 'like') AS likes
		  FROM contents c
		 WHERE c.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("content: card meta: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var cover *string
		var likes int64
		if err := rows.Scan(&id, &cover, &likes); err != nil {
			return nil, fmt.Errorf("content: scan card meta: %w", err)
		}
		meta := CardMeta{Likes: likes}
		if cover != nil {
			meta.Cover = *cover
		}
		result[id] = meta
	}
	return result, rows.Err()
}

// AuthorOf 用于列表补作者信息（喜欢/收藏列表展示他人作品）。
func (r *Repository) AuthorHandles(ctx context.Context, ids []int64) (map[int64]string, error) {
	result := map[int64]string{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.db.Query(ctx, `SELECT id, handle FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return result, fmt.Errorf("author handles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var handle string
		if err := rows.Scan(&id, &handle); err != nil {
			return result, err
		}
		result[id] = handle
	}
	return result, rows.Err()
}
