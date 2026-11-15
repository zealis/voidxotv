// Package danmaku owns the persistent bullet comments attached to a content.
// Each row records the playback position (time_ms) at which it was sent; the
// player fires it again on every loop, and a page reload simply re-fetches it.
package danmaku

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/spam"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

const (
	maxBodyRunes = 30
	maxTimeMS    = 24 * 60 * 60 * 1000
	maxListSize  = 500
)

type Handler struct {
	db   *pgxpool.Pool
	auth *auth.Handler
	log  *slog.Logger
}

func NewHandler(db *pgxpool.Pool, authHandler *auth.Handler, log *slog.Logger) *Handler {
	return &Handler{db: db, auth: authHandler, log: log}
}

type Item struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	TimeMS int64  `json:"time_ms"`
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/contents/{id}/danmaku", h.list)
	mux.Handle("POST /api/v1/contents/{id}/danmaku",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.create))))
}

func contentID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, admin.ErrInvalidInput
	}
	return id, nil
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, httpserver.ErrInvalidJSON):
		httpserver.Error(w, http.StatusBadRequest, "invalid_json", "请求体不合法", requestID)
	case errors.Is(err, admin.ErrInvalidInput):
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", err.Error(), requestID)
	case errors.Is(err, pgx.ErrNoRows):
		httpserver.Error(w, http.StatusNotFound, "not_found", "内容不存在", requestID)
	default:
		h.log.Error("danmaku failed", "error", err, "path", r.URL.Path)
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败", requestID)
	}
}

// list 公开返回某条内容的弹幕，按视频时间点排序，前端按 currentTime 逐条触发。
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT id, body, time_ms FROM danmaku
		WHERE content_id = $1
		ORDER BY time_ms, id
		LIMIT $2`, id, maxListSize)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.Body, &item.TimeMS); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// create 记录一条弹幕；body 限 30 字，time_ms 是发送时的视频播放位置。
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var request struct {
		Body   string `json:"body"`
		TimeMS int64  `json:"time_ms"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	body := strings.TrimSpace(request.Body)
	if body == "" || utf8.RuneCountInString(body) > maxBodyRunes {
		h.fail(w, r, fmt.Errorf("%w: 弹幕最多 30 字", admin.ErrInvalidInput))
		return
	}
	if request.TimeMS < 0 || request.TimeMS > maxTimeMS {
		h.fail(w, r, fmt.Errorf("%w: 弹幕时间点无效", admin.ErrInvalidInput))
		return
	}
	if spam.Guard(w, r, spam.Field{Label: "弹幕", Value: body}) {
		return
	}
	// 只给已发布且可播放的视频发弹幕，避免给草稿 / 下架内容写数据。
	var exists int
	if err := h.db.QueryRow(r.Context(), `
		SELECT 1 FROM contents
		WHERE id = $1 AND type = 'video' AND publish_state = 'published'
		  AND review_status IN ('approved', 'limited')`, id).Scan(&exists); err != nil {
		h.fail(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	var item Item
	if err := h.db.QueryRow(r.Context(), `
		INSERT INTO danmaku (content_id, user_id, body, time_ms)
		VALUES ($1, $2, $3, $4)
		RETURNING id, body, time_ms`,
		id, principal.UserID, body, request.TimeMS).
		Scan(&item.ID, &item.Body, &item.TimeMS); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, map[string]any{"item": item})
}
