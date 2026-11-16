package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/feed"
)

// ErrNotFound is returned when a category slug does not exist or is hidden.
var ErrNotFound = errors.New("search: category not found")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// visiblePredicate returns the shared public-retrieval filter for the given
// viewer placeholder. It mirrors the feed's visibility rules (§3.2) but also
// lets the viewer's own published content surface when friends-only or throttled.
func visiblePredicate(viewerPlaceholder string) string {
	return `
	c.publish_state = 'published' AND c.review_status = 'approved'
	AND c.feed_eligible AND u.status = 'active'
	AND (c.visibility = 'public' OR c.author_id = ` + viewerPlaceholder + `)
	AND (c.recommend_eligible OR c.author_id = ` + viewerPlaceholder + `)`
}

// Search runs one page of retrieval. pattern is the already-escaped
// "%term%" LIKE pattern built by the handler; the query is parameterised, so
// escaping is about correctness, not injection defence.
//
// Results are ordered by a discrete relevance score (exact title > title
// substring > tag/topic hit > description-only) and then by id, which keeps the
// cursor stable without a separate ranking service.
func (r *Repository) Search(ctx context.Context, criteria Criteria, pattern string,
	cursorScore float64, cursorID int64, limit int) ([]feed.Entry, error) {

	durationMin, durationMax := durationBounds(criteria.Duration)
	rows, err := r.db.Query(ctx, `
		WITH matched AS (
		  SELECT c.id, c.slug, c.type, c.title, c.description, c.origin, c.source_url, c.author_id, u.handle,
		         COALESCE(c.published_at, 'epoch'::timestamptz) AS published_at,
		         COALESCE(c.duration_ms, 0) AS duration_ms,
		         CASE
		           WHEN lower(c.title) = lower($1) THEN 1.0
		           WHEN c.title ILIKE $2 THEN 0.8
		           WHEN EXISTS (
		             SELECT 1 FROM content_tags cg JOIN tags g ON g.id = cg.tag_id
		             WHERE cg.content_id = c.id AND g.name ILIKE $2
		           ) OR EXISTS (
		             SELECT 1 FROM content_topics ct JOIN topics t ON t.id = ct.topic_id
		             WHERE ct.content_id = c.id AND t.status = 'available' AND t.name ILIKE $2
		           ) THEN 0.6
		           ELSE 0.4
		         END::float8 AS score
		  FROM contents c
		  JOIN users u ON u.id = c.author_id
		  WHERE `+visiblePredicate("$11")+`
		    AND (
		      c.title ILIKE $2 OR c.description ILIKE $2
		      OR EXISTS (
		        SELECT 1 FROM content_tags cg JOIN tags g ON g.id = cg.tag_id
		        WHERE cg.content_id = c.id AND g.name ILIKE $2
		      )
		      OR EXISTS (
		        SELECT 1 FROM content_topics ct JOIN topics t ON t.id = ct.topic_id
		        WHERE ct.content_id = c.id AND t.status = 'available' AND t.name ILIKE $2
		      )
		    )
		    AND ($3 = '' OR c.type = $3)
		    AND ($4 = 0 OR EXISTS (
		      SELECT 1 FROM content_categories cc
		      WHERE cc.content_id = c.id AND cc.category_id = $4
		    ))
		    AND ($5 = 0 OR (
		      c.type = 'video'
		      AND ($6::bigint = 0 OR COALESCE(c.duration_ms, 0) >= $6::bigint)
		      AND ($7::bigint = 0 OR COALESCE(c.duration_ms, 0) <= $7::bigint)
		    ))
		)
		SELECT id, slug, type, title, description, origin, source_url, author_id, handle,
		       published_at, duration_ms, score
		FROM matched
		WHERE ($8::float8 = 0 AND $9::bigint = 0)
		   OR score < $8::float8 - 1e-9
		   OR (abs(score - $8::float8) <= 1e-9 AND id < $9::bigint)
		ORDER BY score DESC, id DESC
		LIMIT $10`,
		// 全部为位置参数，viewer 作为 $11 传入可见性谓词。
		criteria.Query, pattern, criteria.Type, criteria.CategoryID, criteria.Duration,
		durationMin, durationMax, cursorScore, cursorID, limit, criteria.ViewerID,
	)
	if err != nil {
		return nil, fmt.Errorf("search: query: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// ListByCategory backs /category/[slug]; ordering matches the pagination cursor.
func (r *Repository) ListByCategory(ctx context.Context, categoryID, viewerID, cursor int64,
	limit int) ([]feed.Entry, error) {

	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.slug, c.type, c.title, c.description, c.origin, c.source_url, c.author_id, u.handle,
		       COALESCE(c.published_at, 'epoch'::timestamptz), COALESCE(c.duration_ms, 0),
		       0::float8 AS score
		FROM contents c
		JOIN users u ON u.id = c.author_id
		JOIN content_categories cc ON cc.content_id = c.id
		WHERE cc.category_id = $1
		  AND c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.feed_eligible AND u.status = 'active'
		  AND (c.visibility = 'public' OR c.author_id = $2)
		  AND (c.recommend_eligible OR c.author_id = $2)
		  AND ($3 = 0 OR c.id < $3)
		ORDER BY c.id DESC LIMIT $4`,
		categoryID, viewerID, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("search: category list: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

func scanEntries(rows pgx.Rows) ([]feed.Entry, error) {
	var result []feed.Entry
	for rows.Next() {
		var item feed.Entry
		var publishedAt time.Time
		if err := rows.Scan(&item.ID, &item.Slug, &item.Type, &item.Title, &item.Description,
			&item.Origin, &item.SourceURL, &item.AuthorID, &item.Handle, &publishedAt, &item.DurationMS, &item.Score); err != nil {
			return nil, fmt.Errorf("search: scan: %w", err)
		}
		if publishedAt.Year() > 1970 {
			value := publishedAt
			item.PublishedAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Categories lists publicly browsable categories with their content counts.
func (r *Repository) Categories(ctx context.Context) ([]Category, error) {
	rows, err := r.db.Query(ctx, `
		SELECT cat.id, cat.slug, cat.name, cat.description, cat.position,
		       (SELECT count(*) FROM content_categories cc
		        JOIN contents c ON c.id = cc.content_id
		        JOIN users u ON u.id = c.author_id
		        WHERE cc.category_id = cat.id
		          AND c.publish_state = 'published' AND c.review_status = 'approved'
		          AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		          AND u.status = 'active'
		          AND (c.type = 'image' OR EXISTS (
		            SELECT 1 FROM artifact_manifests am WHERE am.content_id = c.id)))
		FROM categories cat
		WHERE cat.status = 'available'
		ORDER BY cat.position, cat.id`)
	if err != nil {
		return nil, fmt.Errorf("search: categories: %w", err)
	}
	defer rows.Close()

	result := []Category{}
	for rows.Next() {
		var item Category
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description,
			&item.Position, &item.ContentCount); err != nil {
			return nil, fmt.Errorf("search: scan category: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Category returns one available category by slug.
func (r *Repository) Category(ctx context.Context, slug string) (Category, error) {
	var item Category
	err := r.db.QueryRow(ctx, `
		SELECT cat.id, cat.slug, cat.name, cat.description, cat.position,
		       (SELECT count(*) FROM content_categories cc
		        JOIN contents c ON c.id = cc.content_id
		        JOIN users u ON u.id = c.author_id
		        WHERE cc.category_id = cat.id
		          AND c.publish_state = 'published' AND c.review_status = 'approved'
		          AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		          AND u.status = 'active'
		          AND (c.type = 'image' OR EXISTS (
		            SELECT 1 FROM artifact_manifests am WHERE am.content_id = c.id)))
		FROM categories cat
		WHERE cat.slug = $1 AND cat.status = 'available'`, slug).Scan(
		&item.ID, &item.Slug, &item.Name, &item.Description, &item.Position, &item.ContentCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("search: category: %w", err)
	}
	return item, nil
}

// TagsFor loads tags for a page of content ids in one round trip.
func (r *Repository) TagsFor(ctx context.Context, contentIDs []int64) (map[int64][]string, error) {
	if len(contentIDs) == 0 {
		return map[int64][]string{}, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT cg.content_id, g.name
		FROM content_tags cg JOIN tags g ON g.id = cg.tag_id
		WHERE cg.content_id = ANY($1::bigint[])
		ORDER BY g.name`, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("search: tags: %w", err)
	}
	defer rows.Close()

	result := map[int64][]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		result[id] = append(result[id], name)
	}
	return result, rows.Err()
}

// RecordQuery persists one search for hot-term aggregation and latency stats.
func (r *Repository) RecordQuery(ctx context.Context, record QueryRecord) error {
	filters := record.Filters
	if filters == nil {
		filters = map[string]any{}
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO search_queries (user_id, query, normalized, result_count, duration_ms, filters)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		nullIfZero(record.UserID), record.Query, record.Normalized,
		record.ResultCount, record.Duration.Milliseconds(), jsonText(filters))
	if err != nil {
		return fmt.Errorf("search: record query: %w", err)
	}
	return nil
}

// HotQueries returns the most frequent terms of the last 7 days (§9.2).
func (r *Repository) HotQueries(ctx context.Context, limit int) ([]HotQuery, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	rows, err := r.db.Query(ctx, `
		SELECT normalized, count(*) AS hits
		FROM search_queries
		WHERE created_at > now() - interval '7 days'
		  AND char_length(normalized) >= 2
		GROUP BY normalized
		ORDER BY hits DESC, normalized ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("search: hot queries: %w", err)
	}
	defer rows.Close()

	result := []HotQuery{}
	for rows.Next() {
		var item HotQuery
		if err := rows.Scan(&item.Query, &item.Hits); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// durationBounds maps a bucket to an inclusive/exclusive millisecond range.
// A zero bound means "unbounded" on that side.
func durationBounds(bucket int) (int64, int64) {
	switch bucket {
	case 1:
		return 0, 60_000
	case 2:
		return 60_001, 180_000
	case 3:
		return 180_001, 0
	default:
		return 0, 0
	}
}

func nullIfZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func jsonText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
