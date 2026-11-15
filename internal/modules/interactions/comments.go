package interactions

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/gifs"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 评论管理（§11.1 创作者中心 · 评论管理）：
// 作者可以看自己作品下的评论并删除；评论作者本人也可以撤回自己的评论。

type ManagedComment struct {
	ID           int64     `json:"id"`
	ContentID    int64     `json:"content_id"`
	ContentTitle string    `json:"content_title"`
	ContentSlug  string    `json:"content_slug"`
	ContentType  string    `json:"content_type"`
	Handle       string    `json:"handle"`
	DisplayName  string    `json:"display_name"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h *Handler) myComments(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	var total int64
	if err := h.db.QueryRow(r.Context(), `
		SELECT count(*) FROM comments cm JOIN contents c ON c.id = cm.content_id
		WHERE c.author_id = $1 AND cm.status = 'visible' AND c.publish_state <> 'deleted'`,
		principal.UserID).Scan(&total); err != nil {
		h.fail(w, r, err)
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT cm.id, cm.content_id, c.title, c.slug, c.type, u.handle,
		       COALESCE(NULLIF(btrim(u.display_name), ''), u.handle),
		       cm.body, cm.created_at
		FROM comments cm
		JOIN contents c ON c.id = cm.content_id
		JOIN users u ON u.id = cm.user_id
		WHERE c.author_id = $1 AND cm.status = 'visible' AND c.publish_state <> 'deleted'
		ORDER BY cm.created_at DESC, cm.id DESC
		LIMIT $2 OFFSET $3`, principal.UserID, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []ManagedComment{}
	for rows.Next() {
		var item ManagedComment
		if err := rows.Scan(&item.ID, &item.ContentID, &item.ContentTitle, &item.ContentSlug,
			&item.ContentType, &item.Handle, &item.DisplayName, &item.Body, &item.CreatedAt); err != nil {
			h.fail(w, r, err)
			return
		}
		// 评论管理是纯文本列表，GIF 标记换成 [GIF]，别让运营看到一串 URL。
		item.Body = gifs.StripMarkers(item.Body)
		if item.Body == "" {
			item.Body = "[GIF]"
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "has_more": int64(offset+len(items)) < total,
	})
}

func (h *Handler) removeComment(w http.ResponseWriter, r *http.Request) {
	// 作者删评论同样要求二次确认，避免会话被劫持后批量清空讨论。
	if !h.auth.RequireStepUp(w, r) {
		return
	}
	contentID, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	commentID, err := strconv.ParseInt(r.PathValue("commentID"), 10, 64)
	if err != nil || commentID <= 0 {
		h.fail(w, r, admin.ErrInvalidInput)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())

	var commentUser, authorID int64
	err = h.db.QueryRow(r.Context(), `
		SELECT cm.user_id, c.author_id
		FROM comments cm JOIN contents c ON c.id = cm.content_id
		WHERE cm.id = $1 AND cm.content_id = $2`, commentID, contentID).Scan(&commentUser, &authorID)
	if err == pgx.ErrNoRows {
		httpserver.Error(w, http.StatusNotFound, "not_found", "评论不存在",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// 只有内容作者或评论作者本人可以删除。
	if principal.UserID != authorID && principal.UserID != commentUser {
		httpserver.Error(w, http.StatusForbidden, "forbidden", "无权删除这条评论",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if _, err := h.db.Exec(r.Context(), `
		UPDATE comments SET status = 'removed' WHERE id = $1 AND status <> 'removed'`,
		commentID); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"removed": true})
}
