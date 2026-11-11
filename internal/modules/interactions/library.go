package interactions

// 观看进度与稍后再看（§3.1 watch_progress / §4.1 /library / §9.1 进度上报）。
// 进度是可丢失数据：写入走 upsert 尽力而为，历史与续播从 PostgreSQL 读取。

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type LibraryItem struct {
	ContentID    int64  `json:"content_id"`
	Slug         string `json:"slug"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	AuthorID     int64  `json:"author_id"`
	AuthorHandle string `json:"author_handle"`
	PosterURL    string `json:"poster_url,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
	// PositionMS 是最近一次上报的播放位置（历史 / 续播用）。
	PositionMS int64     `json:"position_ms"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// saveProgress upsert 播放位置；内容不存在返回 false（不影响响应语义，仍 200）。
func (h *Handler) saveProgress(w http.ResponseWriter, r *http.Request) (bool, error) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id, err := contentID(r)
	if err != nil {
		return false, err
	}
	var request struct {
		PositionMS int64 `json:"position_ms"`
		DurationMS int64 `json:"duration_ms"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		return false, err
	}
	if request.PositionMS < 0 || request.DurationMS < 0 || request.PositionMS > 24*3600*1000 {
		return false, admin.ErrInvalidInput
	}
	tag, err := h.db.Exec(r.Context(), `
		INSERT INTO watch_progress (user_id, content_id, position_ms, duration_ms, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, content_id) DO UPDATE SET
		  position_ms = EXCLUDED.position_ms,
		  duration_ms = EXCLUDED.duration_ms,
		  updated_at = now()`,
		principal.UserID, id, request.PositionMS, request.DurationMS)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (h *Handler) reportProgress(w http.ResponseWriter, r *http.Request) {
	ok, err := h.saveProgress(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"saved": ok})
}

// myHistory 是 /library 的「历史」分区：按最近观看排序，附续播位置。
func (h *Handler) myHistory(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 30
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM watch_progress WHERE user_id = $1`, principal.UserID).Scan(&total); err != nil {
		h.fail(w, r, err)
		return
	}
	// 封面不在 SQL 里拼：媒体地址需要签名/MediaURL，前端用类型回退卡呈现。
	rows, err := h.db.Query(r.Context(), `
		SELECT wp.content_id, c.slug, c.type, c.title, c.author_id, u.handle,
		       wp.duration_ms, wp.position_ms, wp.updated_at
		FROM watch_progress wp
		JOIN contents c ON c.id = wp.content_id AND c.publish_state <> 'deleted'
		JOIN users u ON u.id = c.author_id
		WHERE wp.user_id = $1
		ORDER BY wp.updated_at DESC
		LIMIT $2 OFFSET $3`, principal.UserID, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := []LibraryItem{}
	for rows.Next() {
		var item LibraryItem
		if err := rows.Scan(&item.ContentID, &item.Slug, &item.Type, &item.Title,
			&item.AuthorID, &item.AuthorHandle,
			&item.DurationMS, &item.PositionMS, &item.UpdatedAt); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	h.attachPosters(r.Context(), items)
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "has_more": int64(offset+len(items)) < total,
	})
}

// contentProgress 给续播用：进入详情时取一次最近位置。
func (h *Handler) contentProgress(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var position, duration int64
	err = h.db.QueryRow(r.Context(), `
		SELECT position_ms, duration_ms FROM watch_progress
		WHERE user_id = $1 AND content_id = $2`, principal.UserID, id).
		Scan(&position, &duration)
	if err == pgx.ErrNoRows {
		httpserver.JSON(w, http.StatusOK, map[string]any{"position_ms": 0, "duration_ms": 0})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"position_ms": position, "duration_ms": duration})
}

// ── 稍后再看 ─────────────────────────────────────────────────────

func (h *Handler) toggleWatchlist(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// 幂等切换：在则删，不在则加；返回当前状态供前端回显。
	var exists bool
	if err := h.db.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM watchlist WHERE user_id = $1 AND content_id = $2)`,
		principal.UserID, id).Scan(&exists); err != nil {
		h.fail(w, r, err)
		return
	}
	if exists {
		if _, err := h.db.Exec(r.Context(),
			`DELETE FROM watchlist WHERE user_id = $1 AND content_id = $2`,
			principal.UserID, id); err != nil {
			h.fail(w, r, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]bool{"saved": false})
		return
	}
	if _, err := h.db.Exec(r.Context(), `
		INSERT INTO watchlist (user_id, content_id) VALUES ($1, $2)
		ON CONFLICT (user_id, content_id) DO NOTHING`, principal.UserID, id); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// myWatchlist 是 /library 的「稍后再看」分区。
func (h *Handler) myWatchlist(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 30
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM watchlist WHERE user_id = $1`, principal.UserID).Scan(&total); err != nil {
		h.fail(w, r, err)
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT w.content_id, c.slug, c.type, c.title, c.author_id, u.handle,
		       COALESCE(c.duration_ms, 0), 0, w.created_at
		FROM watchlist w
		JOIN contents c ON c.id = w.content_id AND c.publish_state <> 'deleted'
		JOIN users u ON u.id = c.author_id
		WHERE w.user_id = $1
		ORDER BY w.created_at DESC
		LIMIT $2 OFFSET $3`, principal.UserID, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := []LibraryItem{}
	for rows.Next() {
		var item LibraryItem
		if err := rows.Scan(&item.ContentID, &item.Slug, &item.Type, &item.Title,
			&item.AuthorID, &item.AuthorHandle,
			&item.DurationMS, &item.PositionMS, &item.UpdatedAt); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	h.attachPosters(r.Context(), items)
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "has_more": int64(offset+len(items)) < total,
	})
}

// watchlistState 给沉浸流操作按钮回显：当前作品是否已在稍后再看。
func (h *Handler) watchlistState(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var exists bool
	if err := h.db.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM watchlist WHERE user_id = $1 AND content_id = $2)`,
		principal.UserID, id).Scan(&exists); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"saved": exists})
}

// attachPosters 给历史 / 稍后再看卡片补封面：图集取第一张图，
// 视频取产物清单里的 poster（其次 cover）。与 feed.images 同口径：
// driver=s3 需要 MediaURL 签名，local_temp 的 demo 路径直接用。
// 失败只记日志，卡片回退到类型占位。
func (h *Handler) attachPosters(ctx context.Context, items []LibraryItem) {
	if h.store == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ContentID)
	}
	rows, err := h.db.Query(ctx, `
		SELECT c.id,
		       COALESCE(
		         (SELECT s.driver || ':' || s.object_key
		            FROM media_assets m
		            JOIN storage_objects s ON s.media_asset_id = m.id
		           WHERE m.content_id = c.id AND m.kind = 'image'
		           ORDER BY m.position, m.id
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
		       ) AS cover
		  FROM contents c
		 WHERE c.id = ANY($1)`, ids)
	if err != nil {
		h.log.Warn("interactions: library posters", "error", err)
		return
	}
	defer rows.Close()
	covers := make(map[int64]string, len(items))
	for rows.Next() {
		var id int64
		var cover *string
		if err := rows.Scan(&id, &cover); err != nil {
			h.log.Warn("interactions: scan library poster", "error", err)
			return
		}
		if cover != nil && *cover != "" {
			covers[id] = *cover
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Warn("interactions: library posters", "error", err)
		return
	}
	for i := range items {
		address := covers[items[i].ContentID]
		driver, key, found := strings.Cut(address, ":")
		if !found || key == "" {
			continue
		}
		if driver != "s3" {
			items[i].PosterURL = key
			continue
		}
		if url, err := h.store.MediaURL(ctx, key); err == nil {
			items[i].PosterURL = url
		}
	}
}
