// Package content · 作者侧话题能力（UI 规范 §8.1 字段规格 / 技术规范 §4.7）。
// 用户自建话题先进入 pending，审核通过（available）后才可被内容关联与公开检索；
// 下线话题保留既有 content_topics 关联，但不再出现在候选与公开查询里。
package content

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

const maxContentTopics = 8

var (
	ErrTopicName        = errors.New("话题名称需为 1–24 个字符，且不能包含控制字符")
	ErrTopicLimit       = fmt.Errorf("单条作品最多关联 %d 个话题", maxContentTopics)
	ErrTopicUnavailable = errors.New("话题尚未通过审核或已下线，不能关联")
	ErrTopicNotFound    = errors.New("话题不存在")
)

// Topic 是发布页候选 / 已选标签的统一形状。
type Topic struct {
	ID     int64  `json:"id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// TopicCard 是话题列表页的卡片：名称 + 公开作品数（§4.1 /topic）。
type TopicCard struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	ContentCount int64  `json:"content_count"`
}

func normalizeTopicName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if n := utf8.RuneCountInString(name); n == 0 || n > 24 {
		return "", ErrTopicName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrTopicName
		}
	}
	return name, nil
}

// ── Repository ──────────────────────────────────────────────────

// ListAvailableTopics 返回全部可公开浏览的话题（§4.1 /topic 话题列表页）：
// 仅 available，按关联作品数降序；带计数与封面信息供列表卡呈现。
func (r *Repository) ListAvailableTopics(ctx context.Context, limit int) ([]TopicCard, error) {
	if limit <= 0 || limit > 100 {
		limit = 60
	}
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.slug, t.name, count(ct.content_id)
		FROM topics t
		LEFT JOIN content_topics ct ON ct.topic_id = t.id
		    AND ct.content_id IN (SELECT id FROM contents
		      WHERE publish_state = 'published' AND review_status = 'approved')
		WHERE t.status = 'available'
		GROUP BY t.id, t.slug, t.name
		ORDER BY count(ct.content_id) DESC, t.id ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("content: list topics: %w", err)
	}
	defer rows.Close()
	items := []TopicCard{}
	for rows.Next() {
		var item TopicCard
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.ContentCount); err != nil {
			return nil, fmt.Errorf("content: scan topics: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// listTopics 公开话题列表（§4.1 /topic）：只出 available 话题，带公开作品计数。
func (h *Handler) listTopics(w http.ResponseWriter, req *http.Request) {
	limit := 60
	if raw := req.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	items, err := h.svc.ListAvailableTopics(req.Context(), limit)
	if err != nil {
		h.writeError(w, req, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (r *Repository) SuggestTopics(ctx context.Context, query string, limit int) ([]Topic, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	pattern := "%" + strings.TrimSpace(query) + "%"
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.status
		FROM topics t
		WHERE t.status = 'available'
		  AND ($1 = '%%' OR t.name ILIKE $1 OR t.slug ILIKE $1)
		ORDER BY (SELECT count(*) FROM content_topics ct WHERE ct.topic_id = t.id) DESC, t.id ASC
		LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("content: suggest topics: %w", err)
	}
	defer rows.Close()
	items := []Topic{}
	for rows.Next() {
		var item Topic
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Status); err != nil {
			return nil, fmt.Errorf("content: scan topics: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreateUserTopic 幂等：同名话题已存在时直接返回，避免重复提交待审。
func (r *Repository) CreateUserTopic(ctx context.Context, userID int64, name string) (Topic, bool, error) {
	var existing Topic
	err := r.db.QueryRow(ctx, `
		SELECT id, slug, name, status FROM topics WHERE name = $1`, name).Scan(
		&existing.ID, &existing.Slug, &existing.Name, &existing.Status)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Topic{}, false, fmt.Errorf("content: lookup topic: %w", err)
	}
	// 中文名不能作为 slug，用不可枚举的随机标识，后台仍以 name 展示与审核。
	suffix, err := NewSlug()
	if err != nil {
		return Topic{}, false, err
	}
	slug := "user-" + suffix
	var created Topic
	err = r.db.QueryRow(ctx, `
		INSERT INTO topics (slug, name, status, creator_id)
		VALUES ($1, $2, 'pending', $3)
		RETURNING id, slug, name, status`, slug, name, userID).Scan(
		&created.ID, &created.Slug, &created.Name, &created.Status)
	if err != nil {
		return Topic{}, false, fmt.Errorf("content: create user topic: %w", err)
	}
	return created, true, nil
}

// ContentTopics 返回作品当前关联的话题（含下线话题，供编辑页回填）。
func (r *Repository) ContentTopics(ctx context.Context, contentID int64) ([]Topic, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.status
		FROM content_topics ct JOIN topics t ON t.id = ct.topic_id
		WHERE ct.content_id = $1 ORDER BY t.id`, contentID)
	if err != nil {
		return nil, fmt.Errorf("content: list content topics: %w", err)
	}
	defer rows.Close()
	items := []Topic{}
	for rows.Next() {
		var item Topic
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Status); err != nil {
			return nil, fmt.Errorf("content: scan content topics: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SetContentTopics 在单个事务里替换关联：先校验作品归属与状态，
// 再校验每个话题都是 available，最后整批替换（幂等、去重）。
func (r *Repository) SetContentTopics(ctx context.Context, contentID, authorID int64, topicIDs []int64) ([]Topic, error) {
	seen := map[int64]bool{}
	unique := make([]int64, 0, len(topicIDs))
	for _, id := range topicIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) > maxContentTopics {
		return nil, ErrTopicLimit
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var state string
	if err := tx.QueryRow(ctx, `
		SELECT publish_state FROM contents
		WHERE id = $1 AND author_id = $2 AND publish_state <> 'deleted' FOR UPDATE`,
		contentID, authorID).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("content: lock owned content: %w", err)
	}
	if state != "draft" && state != "scheduled" {
		return nil, ErrNotDraft
	}

	for _, id := range unique {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM topics WHERE id = $1`, id).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrTopicNotFound
			}
			return nil, fmt.Errorf("content: check topic: %w", err)
		}
		if status != "available" {
			return nil, ErrTopicUnavailable
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM content_topics WHERE content_id = $1`, contentID); err != nil {
		return nil, fmt.Errorf("content: clear content topics: %w", err)
	}
	for _, id := range unique {
		if _, err := tx.Exec(ctx, `
			INSERT INTO content_topics (content_id, topic_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, contentID, id); err != nil {
			return nil, fmt.Errorf("content: attach topic: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("content: commit topics: %w", err)
	}
	return r.ContentTopics(ctx, contentID)
}

// ── Service ─────────────────────────────────────────────────────

func (s *Service) SuggestTopics(ctx context.Context, query string, limit int) ([]Topic, error) {
	return s.repo.SuggestTopics(ctx, query, limit)
}

func (s *Service) ListAvailableTopics(ctx context.Context, limit int) ([]TopicCard, error) {
	return s.repo.ListAvailableTopics(ctx, limit)
}

func (s *Service) CreateUserTopic(ctx context.Context, userID int64, rawName string) (Topic, bool, error) {
	name, err := normalizeTopicName(rawName)
	if err != nil {
		return Topic{}, false, err
	}
	return s.repo.CreateUserTopic(ctx, userID, name)
}

func (s *Service) ContentTopics(ctx context.Context, contentID, authorID int64) ([]Topic, error) {
	if _, err := s.Owned(ctx, contentID, authorID); err != nil {
		return nil, err
	}
	return s.repo.ContentTopics(ctx, contentID)
}

func (s *Service) SetContentTopics(ctx context.Context, contentID, authorID int64, topicIDs []int64) ([]Topic, error) {
	return s.repo.SetContentTopics(ctx, contentID, authorID, topicIDs)
}

// ── Handler ─────────────────────────────────────────────────────

func (h *Handler) registerTopicRoutes(mux *http.ServeMux) {
	// 候选只读、需登录；写操作统一走 guarded（先鉴权再校验 CSRF）。
	mux.HandleFunc("GET /api/v1/topics", h.listTopics)
	mux.Handle("GET /api/v1/topics/suggest", h.auth.RequireAuth(http.HandlerFunc(h.suggestTopics)))
	mux.Handle("POST /api/v1/topics", h.guarded(h.createUserTopic))
	mux.Handle("GET /api/v1/contents/{id}/topics", h.auth.RequireAuth(http.HandlerFunc(h.getContentTopics)))
	mux.Handle("PUT /api/v1/contents/{id}/topics", h.guarded(h.setContentTopics))
}

func (h *Handler) suggestTopics(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil {
			limit = 10
		}
	}
	items, err := h.svc.SuggestTopics(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createUserTopic(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	item, created, err := h.svc.CreateUserTopic(r.Context(), principal.UserID, request.Name)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpserver.JSON(w, status, item)
}

func (h *Handler) getContentTopics(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	items, err := h.svc.ContentTopics(r.Context(), id, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) setContentTopics(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TopicIDs []int64 `json:"topic_ids"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	items, err := h.svc.SetContentTopics(r.Context(), id, principal.UserID, request.TopicIDs)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items})
}
