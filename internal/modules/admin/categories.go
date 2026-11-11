package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 分类属于「系统设置」下的配置（§4.7），因此复用 settings 读写权限。

type AdminCategory struct {
	ID           int64     `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Position     int       `json:"position"`
	Status       string    `json:"status"`
	ContentCount int64     `json:"content_count"`
	CreatedAt    time.Time `json:"created_at"`
}

type CategoryInput struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Position    int    `json:"position"`
	Status      string `json:"status"`
}

// Categories lists every category including hidden ones (运营视角).
func (r *Repository) Categories(ctx context.Context) ([]AdminCategory, error) {
	rows, err := r.db.Query(ctx, `
		SELECT cat.id, cat.slug, cat.name, cat.description, cat.position, cat.status,
		       (SELECT count(*) FROM content_categories cc WHERE cc.category_id = cat.id),
		       cat.created_at
		FROM categories cat
		ORDER BY cat.position, cat.id`)
	if err != nil {
		return nil, fmt.Errorf("admin: categories: %w", err)
	}
	defer rows.Close()

	result := []AdminCategory{}
	for rows.Next() {
		var item AdminCategory
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description,
			&item.Position, &item.Status, &item.ContentCount, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UpsertCategory creates or updates one category. Slug is the stable key used by
// public URLs, so it is validated and never silently regenerated.
func (r *Repository) UpsertCategory(ctx context.Context, input CategoryInput) (AdminCategory, error) {
	slug := strings.TrimSpace(strings.ToLower(input.Slug))
	name := strings.TrimSpace(input.Name)
	status := input.Status
	if status != "available" && status != "hidden" {
		status = "available"
	}
	if slug == "" || name == "" {
		return AdminCategory{}, fmt.Errorf("%w: 分类 slug 与名称必填", ErrInvalidInput)
	}
	if !validCategorySlug(slug) {
		return AdminCategory{}, fmt.Errorf("%w: slug 仅允许小写字母、数字与连字符", ErrInvalidInput)
	}
	if utf8.RuneCountInString(name) > 20 {
		return AdminCategory{}, fmt.Errorf("%w: 分类名称不超过 20 字", ErrInvalidInput)
	}
	if utf8.RuneCountInString(input.Description) > 120 {
		return AdminCategory{}, fmt.Errorf("%w: 分类说明不超过 120 字", ErrInvalidInput)
	}

	var item AdminCategory
	if input.ID > 0 {
		err := r.db.QueryRow(ctx, `
			UPDATE categories SET name = $2, description = $3, position = $4,
			  status = $5, updated_at = now()
			WHERE id = $1
			RETURNING id, slug, name, description, position, status, created_at`,
			input.ID, name, input.Description, input.Position, status).Scan(
			&item.ID, &item.Slug, &item.Name, &item.Description,
			&item.Position, &item.Status, &item.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return AdminCategory{}, ErrNotFound
		}
		if err != nil {
			return AdminCategory{}, fmt.Errorf("admin: update category: %w", err)
		}
		return item, nil
	}

	err := r.db.QueryRow(ctx, `
		INSERT INTO categories (slug, name, description, position, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, slug, name, description, position, status, created_at`,
		slug, name, input.Description, input.Position, status).Scan(
		&item.ID, &item.Slug, &item.Name, &item.Description,
		&item.Position, &item.Status, &item.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return AdminCategory{}, fmt.Errorf("%w: 分类 slug 已存在", ErrConflict)
		}
		return AdminCategory{}, fmt.Errorf("admin: create category: %w", err)
	}
	return item, nil
}

// SetCategoryStatus hides or re-publishes a category. Hiding never deletes the
// content relations, so re-enabling restores the previous listing.
func (r *Repository) SetCategoryStatus(ctx context.Context, id int64, status string) (AdminCategory, error) {
	if status != "available" && status != "hidden" {
		return AdminCategory{}, fmt.Errorf("%w: 未知分类状态", ErrInvalidInput)
	}
	var item AdminCategory
	err := r.db.QueryRow(ctx, `
		UPDATE categories SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, slug, name, description, position, status, created_at`,
		id, status).Scan(&item.ID, &item.Slug, &item.Name, &item.Description,
		&item.Position, &item.Status, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminCategory{}, ErrNotFound
	}
	if err != nil {
		return AdminCategory{}, fmt.Errorf("admin: category status: %w", err)
	}
	return item, nil
}

// AssignCategory attaches or detaches one content↔category edge and emits an
// outbox event carrying the previous category slugs, so Delivery can invalidate
// both the old and the new category page (§10.3 关联触发).
func (r *Repository) AssignCategory(ctx context.Context, contentID, categoryID int64,
	attach bool) ([]string, error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var slug, contentSlug string
	if err := tx.QueryRow(ctx, `SELECT slug, (SELECT slug FROM contents WHERE id = $1)
		FROM categories WHERE id = $2`, contentID, categoryID).Scan(&slug, &contentSlug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	previous, err := categorySlugsTx(ctx, tx, contentID)
	if err != nil {
		return nil, err
	}

	if attach {
		_, err = tx.Exec(ctx, `
			INSERT INTO content_categories (content_id, category_id)
			VALUES ($1, $2) ON CONFLICT (content_id, category_id) DO NOTHING`,
			contentID, categoryID)
	} else {
		_, err = tx.Exec(ctx, `
			DELETE FROM content_categories WHERE content_id = $1 AND category_id = $2`,
			contentID, categoryID)
	}
	if err != nil {
		return nil, fmt.Errorf("admin: assign category: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_key, event_type, payload)
		VALUES ($1, 'content.categories_changed',
		  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text,
		    'prev_categories', $4::jsonb))
		ON CONFLICT (event_key) DO NOTHING`,
		fmt.Sprintf("content.categories_changed:%d:%d:%d:%t", contentID, categoryID, time.Now().UnixNano(), attach),
		contentID, contentSlug, jsonArray(previous)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return previous, nil
}

func categorySlugsTx(ctx context.Context, tx pgx.Tx, contentID int64) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT cat.slug FROM content_categories cc
		JOIN categories cat ON cat.id = cc.category_id
		WHERE cc.content_id = $1`, contentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		result = append(result, slug)
	}
	return result, rows.Err()
}

func validCategorySlug(slug string) bool {
	for _, symbol := range slug {
		if (symbol >= 'a' && symbol <= 'z') || (symbol >= '0' && symbol <= '9') || symbol == '-' {
			continue
		}
		return false
	}
	return len(slug) <= 40
}

func jsonArray(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// ContentCategoryIDs returns the category ids currently attached to a content.
func (r *Repository) ContentCategoryIDs(ctx context.Context, contentID int64) ([]int64, error) {
	rows, err := r.db.Query(ctx, `
		SELECT category_id FROM content_categories WHERE content_id = $1 ORDER BY category_id`,
		contentID)
	if err != nil {
		return nil, fmt.Errorf("admin: content categories: %w", err)
	}
	defer rows.Close()
	result := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
