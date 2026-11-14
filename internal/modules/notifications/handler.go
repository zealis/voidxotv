// Package notifications exposes the in-app notification inbox (§9.4 / §9.5:
// 审核结论、申诉结果、举报反馈都必须让作者/举报人看到).
// 列表按 §9.1 三分组（互动 / 关注 / 系统）过滤，支持单条删除与未读分桶。
package notifications

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/platform/objectstore"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type Notification struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Category  string     `json:"category"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// ActorAvatarURL 是触发者头像（互动 / 关注通知才有）；系统通知为空，
	// 前端回退到分组图标（§9.1 列表项：头像/图标）。
	ActorAvatarURL string `json:"actor_avatar_url,omitempty"`
	// actorID 仅供 attachActors 批量补全使用，不外泄。
	actorID int64
}

type Handler struct {
	db    *pgxpool.Pool
	auth  *auth.Handler
	log   *slog.Logger
	store *objectstore.Store
}

func NewHandler(db *pgxpool.Pool, authHandler *auth.Handler, log *slog.Logger) *Handler {
	return &Handler{db: db, auth: authHandler, log: log}
}

// SetImageStore 注入对象存储；未注入时列表正常返回，只是触发者头像留空走图标回退。
func (h *Handler) SetImageStore(store *objectstore.Store) { h.store = store }

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/notifications", h.auth.RequireAuth(http.HandlerFunc(h.list)))
	// 底栏小粉点只要一个数字：独立端点，避免每个页面挂载时都拉一遍列表。
	mux.Handle("GET /api/v1/notifications/unread", h.auth.RequireAuth(http.HandlerFunc(h.unreadOnly)))
	// 先鉴权再校验 CSRF：RequireCSRF 依赖上下文中的 principal。
	mux.Handle("POST /api/v1/notifications/read", h.guarded(h.markRead))
	// 单条删除（§9.1 左滑删除）的非手势等价入口走同一个端点。
	mux.Handle("DELETE /api/v1/notifications/{id}", h.guarded(h.remove))
}

// unreadOnly 只返回未读数与分桶，不查列表：底栏每页都要用，必须便宜。
func (h *Handler) unreadOnly(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	unread, buckets, err := h.unreadCounts(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"unread": unread, "unread_by_category": buckets,
	})
}

// guarded 固定「先鉴权再校验 CSRF」的顺序：RequireCSRF 依赖上下文里的 principal。
func (h *Handler) guarded(fn func(http.ResponseWriter, *http.Request)) http.Handler {
	return h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(fn)))
}

// validCategory 校验列表过滤参数，取值域与库里的 category 一致（§9.1 三分组）。
func validCategory(value string) bool {
	return value == KindInteraction || value == KindFollow || value == KindSystem
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if cursor < 0 {
		cursor = 0
	}
	group := strings.TrimSpace(r.URL.Query().Get("category"))
	if group != "" && !validCategory(group) {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input",
			"category 必须是 interaction / follow / system",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT id, kind, category, title, body, read_at, created_at, COALESCE(actor_id, 0)
		FROM user_notifications
		WHERE user_id = $1 AND deleted_at IS NULL
		  AND ($2::text = '' OR category = $2::text)
		  AND ($3 = 0 OR id < $3)
		ORDER BY id DESC LIMIT $4`, principal.UserID, group, cursor, limit+1)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []Notification{}
	for rows.Next() {
		var item Notification
		if err := rows.Scan(&item.ID, &item.Kind, &item.Category, &item.Title, &item.Body,
			&item.ReadAt, &item.CreatedAt, &item.actorID); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	h.attachActors(r.Context(), items)

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var next any
	if hasMore && len(items) > 0 {
		next = items[len(items)-1].ID
	}

	unread, buckets, err := h.unreadCounts(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next, "count": len(items),
		"unread": unread, "unread_by_category": buckets,
	})
}

type readRequest struct {
	IDs []int64 `json:"ids"`
	// Category 非空时只标记该分组（§9.1 分组内的「全部已读」），空表示全部。
	Category string `json:"category,omitempty"`
}

// markRead marks the given notifications (or all unread, optionally per group) as read.
func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var request readRequest
	if r.ContentLength > 0 {
		if err := httpserver.DecodeJSON(r, &request); err != nil {
			httpserver.Error(w, http.StatusBadRequest, "invalid_input", err.Error(),
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
	}
	if request.Category != "" && !validCategory(request.Category) {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input",
			"category 必须是 interaction / follow / system",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}

	var err error
	if len(request.IDs) == 0 {
		_, err = h.db.Exec(r.Context(), `
			UPDATE user_notifications SET read_at = now()
			WHERE user_id = $1 AND read_at IS NULL AND deleted_at IS NULL
			  AND ($2::text = '' OR category = $2::text)`,
			principal.UserID, request.Category)
	} else {
		_, err = h.db.Exec(r.Context(), `
			UPDATE user_notifications SET read_at = now()
			WHERE user_id = $1 AND read_at IS NULL AND deleted_at IS NULL
			  AND id = ANY($2::bigint[])`,
			principal.UserID, request.IDs)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	unread, buckets, err := h.unreadCounts(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"unread": unread, "unread_by_category": buckets,
	})
}

// remove 软删一条通知：列表与未读计数从此排除；行保留以备审计与误删恢复的演进。
// 不存在 / 已删除 / 别人的通知一律 404，不泄露存在性。
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", "通知 ID 不合法",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	tag, err := h.db.Exec(r.Context(), `
		UPDATE user_notifications SET deleted_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpserver.Error(w, http.StatusNotFound, "not_found", "通知不存在或已删除",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	unread, buckets, err := h.unreadCounts(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"deleted": true, "unread": unread, "unread_by_category": buckets,
	})
}

// unreadCounts 返回未读总数与 §9.1 三分组的未读分桶。
// map 三键恒齐（没有未读的分组为 0），前端免判空。
func (h *Handler) unreadCounts(ctx context.Context, userID int64) (int64, map[string]int64, error) {
	buckets := map[string]int64{KindInteraction: 0, KindFollow: 0, KindSystem: 0}
	rows, err := h.db.Query(ctx, `
		SELECT category, count(*) FROM user_notifications
		WHERE user_id = $1 AND read_at IS NULL AND deleted_at IS NULL
		GROUP BY category`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var category string
		var count int64
		if err := rows.Scan(&category, &count); err != nil {
			return 0, nil, err
		}
		buckets[category] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	return total, buckets, nil
}

// attachActors 批量补全触发者头像。与 feed 的做法一致：集中一次查询，
// 避免 N+1；S3 未注入或查询失败时静默回退到分组图标，不影响列表。
func (h *Handler) attachActors(ctx context.Context, items []Notification) {
	if h.store == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.actorID == 0 {
			continue
		}
		if _, ok := seen[item.actorID]; ok {
			continue
		}
		seen[item.actorID] = struct{}{}
		ids = append(ids, item.actorID)
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.db.Query(ctx, `
		SELECT id, avatar_key FROM users
		WHERE id = ANY($1) AND COALESCE(avatar_key, '') <> ''`, ids)
	if err != nil {
		h.log.Warn("notifications: actor avatars", "error", err)
		return
	}
	defer rows.Close()
	keys := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			h.log.Warn("notifications: scan avatar", "error", err)
			return
		}
		keys[id] = key
	}
	if err := rows.Err(); err != nil {
		h.log.Warn("notifications: actor avatars", "error", err)
		return
	}
	for i := range items {
		key := keys[items[i].actorID]
		if key == "" {
			continue
		}
		if url, err := h.store.MediaURL(ctx, key); err == nil {
			items[i].ActorAvatarURL = url
		}
	}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("notifications error", "error", err, "path", r.URL.Path)
	httpserver.Error(w, http.StatusInternalServerError, "internal_error",
		"unexpected server error", httpserver.RequestIDFromContext(r.Context()))
}
