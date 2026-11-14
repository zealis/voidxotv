package feed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/content"
	"github.com/yejie/voidxotv/internal/platform/nightid"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// List walks the public feed newest-first with an id cursor. The WHERE clause
// matches contents_public_feed_idx exactly so the partial index is usable.
func (r *Repository) List(ctx context.Context, cursor int64, limit int) ([]row, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.slug, c.type, c.title, c.description, c.author_id, u.handle, u.display_name,
		       COALESCE(c.published_at, 'epoch'::timestamptz), COALESCE(c.duration_ms, 0)
		FROM contents c JOIN users u ON u.id = c.author_id
		WHERE c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.visibility = 'public' AND c.feed_eligible
		  -- M4：审核「限流」保留落地页，但退出推荐流（§9.4）。
		  AND c.recommend_eligible
		  AND ($1 = 0 OR c.id < $1)
		ORDER BY c.id DESC LIMIT $2`, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("feed: list: %w", err)
	}
	defer rows.Close()

	var result []row
	for rows.Next() {
		var item row
		var publishedAt time.Time
		if err := rows.Scan(&item.ID, &item.Slug, &item.Type, &item.Title, &item.Description,
			&item.AuthorID, &item.Handle, &item.DisplayName, &publishedAt, &item.DurationMS); err != nil {
			return nil, fmt.Errorf("feed: scan: %w", err)
		}
		if publishedAt.Year() > 1970 {
			value := publishedAt
			item.PublishedAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Avatars 批量取作者头像键。feed 的列表入口很多（推荐 / 关注 / 话题 / 作者 /
// 详情），集中一次查询比在每条 SQL 里各加一列更不容易漏，也避免 N+1。
func (r *Repository) Avatars(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	result := make(map[int64]string, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, avatar_key FROM users
		WHERE id = ANY($1) AND COALESCE(avatar_key, '') <> ''`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("feed: avatars: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("feed: scan avatar: %w", err)
		}
		result[id] = key
	}
	return result, rows.Err()
}

// ListByHandle backs the public creator page; visibility rules are identical to
// the home feed so a creator page can never expose non-public content.
func (r *Repository) ListByHandle(ctx context.Context, handle string, cursor int64, limit int) ([]row, error) {
	handle = nightid.Normalize(handle)
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.slug, c.type, c.title, c.description, c.author_id, u.handle, u.display_name,
		       COALESCE(c.published_at, 'epoch'::timestamptz), COALESCE(c.duration_ms, 0)
		FROM contents c JOIN users u ON u.id = c.author_id
		WHERE u.handle = $1
		  AND c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		  AND u.status = 'active'
		  AND ($2 = 0 OR c.id < $2)
		ORDER BY c.id DESC LIMIT $3`, handle, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("feed: list by handle: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// ListByTopic backs /topic/[slug]; only available topics are publicly browsable.
func (r *Repository) ListByTopic(ctx context.Context, slug string, cursor int64, limit int) ([]row, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.slug, c.type, c.title, c.description, c.author_id, u.handle, u.display_name,
		       COALESCE(c.published_at, 'epoch'::timestamptz), COALESCE(c.duration_ms, 0)
		FROM contents c
		JOIN users u ON u.id = c.author_id
		JOIN content_topics ct ON ct.content_id = c.id
		JOIN topics t ON t.id = ct.topic_id
		WHERE t.slug = $1 AND t.status = 'available'
		  AND c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		  AND u.status = 'active'
		  AND ($2 = 0 OR c.id < $2)
		ORDER BY c.id DESC LIMIT $3`, slug, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("feed: list by topic: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

func scanRows(rows pgx.Rows) ([]row, error) {
	var result []row
	for rows.Next() {
		var item row
		var publishedAt time.Time
		if err := rows.Scan(&item.ID, &item.Slug, &item.Type, &item.Title, &item.Description,
			&item.AuthorID, &item.Handle, &item.DisplayName, &publishedAt, &item.DurationMS); err != nil {
			return nil, fmt.Errorf("feed: scan row: %w", err)
		}
		if publishedAt.Year() > 1970 {
			value := publishedAt
			item.PublishedAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type CreatorProfile struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	// 简介来自设置页「账号」分组；空字符串表示作者没填。
	Bio       string `json:"bio"`
	AvatarKey string `json:"-"`
	AvatarURL string `json:"avatar_url"`
	// 计数区口径与「我」页面一致（UI 规范 §7 / §10）：获赞只算公开且已通过的作品。
	ContentCount   int64 `json:"content_count"`
	LikeCount      int64 `json:"like_count"`
	FollowingCount int64 `json:"following_count"`
	FollowerCount  int64 `json:"follower_count"`
	// VerifiedCreator 是已认证创作者（users.role = 'creator'），前端在昵称旁挂标签。
	VerifiedCreator bool `json:"verified_creator"`
}

func (r *Repository) Creator(ctx context.Context, handle string) (CreatorProfile, error) {
	var profile CreatorProfile
	var avatar *string
	err := r.db.QueryRow(ctx, `
		SELECT u.handle, u.display_name, u.bio, u.avatar_key, u.role = 'creator',
		       (SELECT count(*) FROM contents c
		        WHERE c.author_id = u.id AND c.publish_state = 'published'
		          AND c.review_status = 'approved' AND c.visibility = 'public'
		          AND c.feed_eligible
		          -- 口径与沉浸流一致：无播放产物的内容不计入作品数（§3.2）。
		          AND (c.type = 'image' OR EXISTS (
		            SELECT 1 FROM artifact_manifests am WHERE am.content_id = c.id))),
		       (SELECT count(*) FROM reactions rc JOIN contents c ON c.id = rc.content_id
		        WHERE c.author_id = u.id AND rc.kind = 'like'
		          AND c.publish_state = 'published' AND c.review_status = 'approved'
		          AND c.visibility = 'public'),
		       (SELECT count(*) FROM follows f WHERE f.follower_id = u.id),
		       (SELECT count(*) FROM follows f WHERE f.followee_id = u.id)
		FROM users u WHERE u.handle = $1 AND u.status = 'active'`, nightid.Normalize(handle)).Scan(
		&profile.Handle, &profile.DisplayName, &profile.Bio, &avatar, &profile.VerifiedCreator,
		&profile.ContentCount, &profile.LikeCount, &profile.FollowingCount, &profile.FollowerCount)
	if err != nil {
		return profile, err
	}
	if avatar != nil {
		profile.AvatarKey = *avatar
	}
	return profile, nil
}

type TopicProfile struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ContentCount int64  `json:"content_count"`
}

func (r *Repository) Topic(ctx context.Context, slug string) (TopicProfile, error) {
	var profile TopicProfile
	err := r.db.QueryRow(ctx, `
		SELECT t.slug, t.name, t.status,
		       (SELECT count(*) FROM content_topics ct
		        JOIN contents c ON c.id = ct.content_id
		        WHERE ct.topic_id = t.id AND c.publish_state = 'published'
		          AND c.review_status = 'approved' AND c.visibility = 'public'
		          AND c.feed_eligible
		          AND (c.type = 'image' OR EXISTS (
		            SELECT 1 FROM artifact_manifests am WHERE am.content_id = c.id)))
		FROM topics t WHERE t.slug = $1`, slug).Scan(
		&profile.Slug, &profile.Name, &profile.Status, &profile.ContentCount)
	return profile, err
}

// Gallery returns ordered image addresses for an image-type content item.
func (r *Repository) Gallery(ctx context.Context, contentID int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.driver, s.object_key
		FROM media_assets m JOIN storage_objects s ON s.media_asset_id = m.id
		WHERE m.content_id = $1 AND m.kind = 'image'
		ORDER BY m.position, m.id`, contentID)
	if err != nil {
		return nil, fmt.Errorf("feed: gallery: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var driver, key string
		if err := rows.Scan(&driver, &key); err != nil {
			return nil, err
		}
		result = append(result, driver+":"+key)
	}
	return result, rows.Err()
}

// InteractionStats 返回一条内容的真实互动计数（点赞 / 收藏 / 可见评论）。
// 供 compose 填充 stats；shares 暂无数据源，恒为 0。
func (r *Repository) InteractionStats(ctx context.Context, contentID int64) (Stats, error) {
	var stats Stats
	err := r.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM reactions WHERE content_id = $1 AND kind = 'like'),
		  (SELECT count(*) FROM reactions WHERE content_id = $1 AND kind = 'favorite'),
		  (SELECT count(*) FROM comments WHERE content_id = $1 AND status = 'visible')`,
		contentID).Scan(&stats.Likes, &stats.Favorites, &stats.Comments)
	if err != nil {
		return Stats{}, fmt.Errorf("feed: interaction stats: %w", err)
	}
	return stats, nil
}

// ViewerMarks 是查看者对一条内容自己的互动标记。
type ViewerMarks struct {
	Liked     bool
	Favorited bool
}

// ViewerReactions 批量返回查看者在这批内容上的点赞 / 收藏标记（§6.2 操作栏回显）。
// 只回真正存在互动记录的条目：没有记录的键缺省，即未点亮。
func (r *Repository) ViewerReactions(ctx context.Context, userID int64,
	contentIDs []int64) (map[int64]ViewerMarks, error) {
	rows, err := r.db.Query(ctx, `
		SELECT content_id, kind FROM reactions
		WHERE user_id = $1 AND kind IN ('like', 'favorite') AND content_id = ANY($2)`,
		userID, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("feed: viewer reactions: %w", err)
	}
	defer rows.Close()

	marks := make(map[int64]ViewerMarks, len(contentIDs))
	for rows.Next() {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return nil, fmt.Errorf("feed: viewer reactions scan: %w", err)
		}
		mark := marks[id]
		if kind == "like" {
			mark.Liked = true
		} else {
			mark.Favorited = true
		}
		marks[id] = mark
	}
	return marks, rows.Err()
}

func (r *Repository) HandleByID(ctx context.Context, userID int64) (string, error) {
	var handle string
	if err := r.db.QueryRow(ctx, `SELECT handle FROM users WHERE id = $1`, userID).Scan(&handle); err != nil {
		return "", fmt.Errorf("feed: author handle: %w", err)
	}
	return handle, nil
}

// Entry is one row of a content listing. Exported so read-side modules
// (search, category pages) reuse the same item composition instead of
// duplicating playback/poster rules.
type Entry struct {
	ID          int64
	Slug        string
	Type        string
	Title       string
	Description string
	AuthorID    int64
	Handle      string
	DisplayName string
	PublishedAt *time.Time
	DurationMS  int64
	// Score 仅规则排序模式使用，作为不透明游标的一部分返回。
	Score float64
}

// row keeps the internal call sites terse; Entry is the exported alias.
type row = Entry

// ListRanked applies the explainable rule ranking from §9.3:
//
//	score = 0.40 · timeDecay(7d half-life)
//	      + 0.25 · heat(plays, saturating)
//	      + 0.15 · likes(100 saturating)
//	      + 0.10 · follow bonus
//	      + 0.10 · interest/topic bonus
//
// The candidate window is bounded by candidateDays, and a per-author cap of 3
// provides the "去重与多样性" step without a separate recommender service.
// viewerID = 0 means an anonymous visitor (no follow bonus).
func (r *Repository) ListRanked(ctx context.Context, viewerID int64, cursorScore float64,
	cursorID int64, limit, candidateDays int) ([]row, error) {
	if candidateDays <= 0 {
		candidateDays = 30
	}
	rows, err := r.db.Query(ctx, `
		WITH candidate AS (
		  SELECT c.id, c.slug, c.type, c.title, c.description, c.author_id, u.handle, u.display_name,
		         COALESCE(c.published_at, 'epoch'::timestamptz) AS published_at,
		         COALESCE(c.duration_ms, 0) AS duration_ms,
		         (
		           -- 时间基准按小时取整：同一小时内的分数稳定，游标翻页不会因
		           -- now() 漂移把边界行重复返回（§9.3 可解释、可复现）。
		           0.40 * exp(-EXTRACT(EPOCH FROM (
		             GREATEST(date_trunc('hour', now()), COALESCE(c.published_at, c.created_at))
		             - COALESCE(c.published_at, c.created_at)
		           )) / 604800.0)
		           + 0.25 * LEAST(1.0, ln(1 + COALESCE(cc.plays, 0)::numeric) / ln(10001.0))
		           + 0.15 * LEAST(1.0, COALESCE(rl.likes, 0)::numeric / 100.0)
		           + 0.10 * CASE WHEN f.follower_id IS NOT NULL THEN 1.0 ELSE 0.0 END
		           + 0.10 * CASE WHEN ct.topic_id IS NOT NULL THEN 1.0 ELSE 0.0 END
		         )::float8 AS score
		  FROM contents c
		  JOIN users u ON u.id = c.author_id
		  LEFT JOIN content_counters cc ON cc.content_id = c.id
		  LEFT JOIN (
		    SELECT content_id, count(*)::int AS likes FROM reactions
		    WHERE kind = 'like' GROUP BY content_id
		  ) rl ON rl.content_id = c.id
		  LEFT JOIN follows f ON f.follower_id = $1 AND f.followee_id = c.author_id
		  LEFT JOIN LATERAL (
		    SELECT ct.topic_id FROM content_topics ct
		    JOIN topics t ON t.id = ct.topic_id AND t.status = 'available'
		    WHERE ct.content_id = c.id LIMIT 1
		  ) ct ON true
		  WHERE c.publish_state = 'published' AND c.review_status = 'approved'
		    AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		    AND u.status = 'active'
		    AND COALESCE(c.published_at, c.created_at) > now() - make_interval(days => $2)
		), diversified AS (
		  SELECT *, row_number() OVER (
		    PARTITION BY author_id ORDER BY score DESC, id DESC
		  ) AS author_rank
		  FROM candidate
		)
		SELECT id, slug, type, title, description, author_id, handle, display_name,
		       published_at, duration_ms, score
		FROM diversified
		WHERE author_rank <= 3
		  AND (
		    ($3::float8 = 0 AND $4::bigint = 0)
		    OR score < $3::float8 - 1e-9
		    OR (abs(score - $3::float8) <= 1e-9 AND id < $4::bigint)
		  )
		ORDER BY score DESC, id DESC
		LIMIT $5`, viewerID, candidateDays, cursorScore, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("feed: list ranked: %w", err)
	}
	defer rows.Close()

	var result []row
	for rows.Next() {
		var item row
		var publishedAt time.Time
		if err := rows.Scan(&item.ID, &item.Slug, &item.Type, &item.Title, &item.Description,
			&item.AuthorID, &item.Handle, &item.DisplayName, &publishedAt, &item.DurationMS,
			&item.Score); err != nil {
			return nil, fmt.Errorf("feed: scan ranked row: %w", err)
		}
		if publishedAt.Year() > 1970 {
			value := publishedAt
			item.PublishedAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r row) asContent() content.Content {
	return content.Content{
		ID: r.ID, Slug: r.Slug, Type: r.Type, Title: r.Title,
		Description: r.Description, AuthorID: r.AuthorID,
		Visibility: "public", PublishState: "published", ReviewStatus: "approved",
		PublishedAt: r.PublishedAt,
	}
}

// ── 关注关系（§9.1 关注通知 / 关注流）──────────────────────────────

// DB exposes the pool for cross-cutting writes (关注通知) that must use the
// same connection as the follow change itself.
func (r *Repository) DB() *pgxpool.Pool { return r.db }

// FollowTargetID resolves an active creator by handle.
// 停用 / 封禁账号按不存在处理，避免关注已失效的人。
func (r *Repository) FollowTargetID(ctx context.Context, handle string) (int64, error) {
	var id int64
	if err := r.db.QueryRow(ctx,
		`SELECT id FROM users WHERE handle = $1 AND status = 'active'`,
		nightid.Normalize(handle)).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// SetFollow is idempotent: 重复关注 / 重复取消都不会报错，只有真正变化时 changed 为真。
func (r *Repository) SetFollow(ctx context.Context, followerID, followeeID int64, value bool) (bool, error) {
	changed := false
	if value {
		tag, err := r.db.Exec(ctx, `
			INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2)
			ON CONFLICT (follower_id, followee_id) DO NOTHING`, followerID, followeeID)
		if err != nil {
			return false, fmt.Errorf("feed: follow: %w", err)
		}
		changed = tag.RowsAffected() > 0
	} else {
		tag, err := r.db.Exec(ctx, `
			DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, followerID, followeeID)
		if err != nil {
			return false, fmt.Errorf("feed: unfollow: %w", err)
		}
		changed = tag.RowsAffected() > 0
	}
	return changed, nil
}

func (r *Repository) CountFollowers(ctx context.Context, userID int64) (int64, error) {
	var count int64
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM follows WHERE followee_id = $1`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("feed: count followers: %w", err)
	}
	return count, nil
}

func (r *Repository) IsFollowing(ctx context.Context, followerID, followeeID int64) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2)`,
		followerID, followeeID).Scan(&exists); err != nil {
		return false, fmt.Errorf("feed: is following: %w", err)
	}
	return exists, nil
}

// FollowingHandles 用于信息流首屏水合：一次拿全量关注列表，避免逐条查询。
func (r *Repository) FollowingHandles(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.handle FROM follows f JOIN users u ON u.id = f.followee_id
		WHERE f.follower_id = $1 ORDER BY f.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("feed: following handles: %w", err)
	}
	defer rows.Close()
	handles := []string{}
	for rows.Next() {
		var handle string
		if err := rows.Scan(&handle); err != nil {
			return nil, err
		}
		handles = append(handles, handle)
	}
	return handles, rows.Err()
}

// ListFollowing 只返回我关注作者的作品，规则与推荐流一致（§9.3 / §9.4）。
func (r *Repository) ListFollowing(ctx context.Context, viewerID, cursor int64, limit int) ([]row, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.slug, c.type, c.title, c.description, c.author_id, u.handle, u.display_name,
		       COALESCE(c.published_at, 'epoch'::timestamptz), COALESCE(c.duration_ms, 0)
		FROM contents c
		JOIN users u ON u.id = c.author_id
		JOIN follows f ON f.followee_id = c.author_id AND f.follower_id = $1
		WHERE c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		  AND ($2 = 0 OR c.id < $2)
		ORDER BY c.id DESC LIMIT $3`, viewerID, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("feed: list following: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}
