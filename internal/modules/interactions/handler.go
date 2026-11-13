// Package interactions owns user-side play events, reactions, comments and
// report submissions. Moderation of those reports lives in the admin module.
package interactions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/notifications"
	"github.com/yejie/voidxotv/internal/modules/stats"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/nightid"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

type Handler struct {
	db       *pgxpool.Pool
	counters *stats.Counters
	auth     *auth.Handler
	reports  *admin.Repository
	log      *slog.Logger
	// store 用于个人库卡片的封面地址；未注入时卡片回退到类型占位。
	store *objectstore.Store
}

// SetImageStore 注入对象存储（与 feed 的封面/图集同口径）。
func (h *Handler) SetImageStore(store *objectstore.Store) { h.store = store }

func NewHandler(db *pgxpool.Pool, counters *stats.Counters, authHandler *auth.Handler,
	reports *admin.Repository, log *slog.Logger) *Handler {
	return &Handler{db: db, counters: counters, auth: authHandler, reports: reports, log: log}
}

func (h *Handler) guarded(fn func(http.ResponseWriter, *http.Request)) http.Handler {
	return h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(fn)))
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/events/play", h.recordPlay)
	mux.Handle("POST /api/v1/contents/{id}/reactions", h.guarded(h.setReaction))
	mux.HandleFunc("GET /api/v1/contents/{id}/reactions", h.getReactions)
	mux.HandleFunc("GET /api/v1/contents/{id}/comments", h.listComments)
	mux.Handle("POST /api/v1/contents/{id}/comments", h.guarded(h.addComment))
	// 楼中楼：顶层评论线程内的回复（§6.5 回复上下文）。
	mux.HandleFunc("GET /api/v1/contents/{id}/comments/{commentID}/replies", h.listReplies)
	// 评论管理（§11.1）：作者查看自己作品下的评论并删除。
	mux.Handle("GET /api/v1/me/comments", h.auth.RequireAuth(http.HandlerFunc(h.myComments)))
	mux.Handle("DELETE /api/v1/contents/{id}/comments/{commentID}",
		h.guarded(h.removeComment))
	mux.Handle("POST /api/v1/reports", h.guarded(h.submitReport))
	// 观看进度与稍后再看（§3.1 / §4.1 /library）。
	mux.Handle("POST /api/v1/contents/{id}/progress", h.guarded(h.reportProgress))
	mux.Handle("GET /api/v1/contents/{id}/progress", h.auth.RequireAuth(http.HandlerFunc(h.contentProgress)))
	mux.Handle("POST /api/v1/contents/{id}/watchlist", h.guarded(h.toggleWatchlist))
	mux.Handle("GET /api/v1/contents/{id}/watchlist", h.auth.RequireAuth(http.HandlerFunc(h.watchlistState)))
	mux.Handle("GET /api/v1/me/history", h.auth.RequireAuth(http.HandlerFunc(h.myHistory)))
	mux.Handle("GET /api/v1/me/watchlist", h.auth.RequireAuth(http.HandlerFunc(h.myWatchlist)))
	// 举报人可查看自己的举报记录与处置结果（§9.5）。
	mux.Handle("GET /api/v1/reports/mine", h.auth.RequireAuth(http.HandlerFunc(h.myReports)))
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
		h.log.Error("interaction failed", "error", err, "path", r.URL.Path)
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败", requestID)
	}
}

func contentID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, admin.ErrInvalidInput
	}
	return id, nil
}

// ── 播放事件（§7.6）───────────────────────────────────────────────

func (h *Handler) recordPlay(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ContentID int64 `json:"content_id"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	if request.ContentID <= 0 {
		h.fail(w, r, admin.ErrInvalidInput)
		return
	}
	var exists int
	if err := h.db.QueryRow(r.Context(), `
		SELECT 1 FROM contents
		WHERE id = $1 AND publish_state = 'published'
		  AND review_status IN ('approved', 'limited')`, request.ContentID).Scan(&exists); err != nil {
		h.fail(w, r, err)
		return
	}
	// 机器流量按风控规则过滤，不进入有效播放统计。
	ua := strings.ToLower(r.UserAgent())
	if strings.Contains(ua, "bot") || strings.Contains(ua, "spider") ||
		strings.Contains(ua, "crawler") {
		httpserver.JSON(w, http.StatusOK, map[string]any{"counted": false, "reason": "bot"})
		return
	}
	viewerKey := anonymousViewerKey(r)
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerKey = "u:" + strconv.FormatInt(principal.UserID, 10)
	}
	counted, err := h.counters.RecordPlay(r.Context(), request.ContentID, viewerKey, time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"counted": counted,
		"dedup_window": "30m"})
}

func anonymousViewerKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	sum := sha256.Sum256([]byte(host + "|" + r.UserAgent()))
	return "a:" + hex.EncodeToString(sum[:8])
}

// ── 点赞 / 收藏（唯一约束保证幂等）───────────────────────────────

func (h *Handler) setReaction(w http.ResponseWriter, r *http.Request) {
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var request struct {
		Kind  string `json:"kind"`
		Value bool   `json:"value"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	if request.Kind != "like" && request.Kind != "favorite" {
		h.fail(w, r, admin.ErrInvalidInput)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	// 只有真正插入（而不是重复点赞）才提醒作者：开关式接口反复调用不该刷通知。
	var inserted bool
	if request.Value {
		var tag pgconn.CommandTag
		tag, err = h.db.Exec(r.Context(), `
			INSERT INTO reactions (user_id, content_id, kind) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, content_id, kind) DO NOTHING`,
			principal.UserID, id, request.Kind)
		inserted = err == nil && tag.RowsAffected() > 0
	} else {
		_, err = h.db.Exec(r.Context(), `
			DELETE FROM reactions WHERE user_id = $1 AND content_id = $2 AND kind = $3`,
			principal.UserID, id, request.Kind)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if inserted {
		// 互动通知（§9.1）：失败只记日志，不影响点赞 / 收藏本身。
		h.notifyReaction(r.Context(), id, principal, request.Kind)
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"kind": request.Kind, "value": request.Value})
}

func (h *Handler) getReactions(w http.ResponseWriter, r *http.Request) {
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var likes, favorites int64
	if err := h.db.QueryRow(r.Context(), `
		SELECT
		  count(*) FILTER (WHERE kind = 'like'),
		  count(*) FILTER (WHERE kind = 'favorite')
		FROM reactions WHERE content_id = $1`, id).Scan(&likes, &favorites); err != nil {
		h.fail(w, r, err)
		return
	}
	result := map[string]any{"likes": likes, "favorites": favorites,
		"liked": false, "favorited": false}
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		rows, err := h.db.Query(r.Context(), `
			SELECT kind FROM reactions WHERE user_id = $1 AND content_id = $2`,
			principal.UserID, id)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var kind string
				if rows.Scan(&kind) == nil {
					result[kind+"d"] = true
				}
			}
		}
	}
	httpserver.JSON(w, http.StatusOK, result)
}

// ── 评论（§6.5 评论抽屉：分页列表、楼中楼回复、@提及通知）──────────

type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	Handle    string    `json:"handle"`
	CreatedAt time.Time `json:"created_at"`
	UserID    int64     `json:"user_id"`
	// ReplyCount 是该顶层评论线程内的可见回复数（仅顶层评论返回）。
	ReplyCount int64 `json:"reply_count"`
}

type Reply struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	Handle    string    `json:"handle"`
	CreatedAt time.Time `json:"created_at"`
	UserID    int64     `json:"user_id"`
	// ReplyTo 是「回复 @某人」的目标作者 handle（回复线程内其他回复时才有）。
	ReplyTo string `json:"reply_to,omitempty"`
}

// listComments 返回顶层评论（倒序游标分页），每条带线程内可见回复数。
func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if cursor < 0 {
		cursor = 0
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT c.id, c.body, u.handle, c.created_at, c.user_id,
		       (SELECT count(*) FROM comments rep
		        WHERE rep.parent_id = c.id AND rep.status = 'visible')
		FROM comments c JOIN users u ON u.id = c.user_id
		WHERE c.content_id = $1 AND c.status = 'visible' AND c.parent_id IS NULL
		  AND ($2 = 0 OR c.id < $2)
		ORDER BY c.id DESC LIMIT $3`, id, cursor, limit+1)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []Comment{}
	for rows.Next() {
		var item Comment
		if err := rows.Scan(&item.ID, &item.Body, &item.Handle, &item.CreatedAt,
			&item.UserID, &item.ReplyCount); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var next any
	if hasMore && len(items) > 0 {
		next = items[len(items)-1].ID
	}

	// total 含回复：评论抽屉标题与 feed 角标用的都是这个口径。
	var total int64
	if err := h.db.QueryRow(r.Context(), `
		SELECT count(*) FROM comments
		WHERE content_id = $1 AND status = 'visible'`, id).Scan(&total); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "next_cursor": next, "count": len(items),
	})
}

// listReplies 返回一条顶层评论的可见回复（正序，游标分页），带「回复 @某人」标识。
func (h *Handler) listReplies(w http.ResponseWriter, r *http.Request) {
	if _, err := contentID(r); err != nil {
		h.fail(w, r, err)
		return
	}
	rootID, err := strconv.ParseInt(r.PathValue("commentID"), 10, 64)
	if err != nil || rootID <= 0 {
		h.fail(w, r, admin.ErrInvalidInput)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if cursor < 0 {
		cursor = 0
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT c.id, c.body, u.handle, c.created_at, c.user_id, COALESCE(rt.handle, '')
		FROM comments c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN users rt ON rt.id = c.reply_to_user_id
		WHERE c.parent_id = $1 AND c.status = 'visible'
		  AND ($2 = 0 OR c.id > $2)
		ORDER BY c.id ASC LIMIT $3`, rootID, cursor, limit+1)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []Reply{}
	for rows.Next() {
		var item Reply
		if err := rows.Scan(&item.ID, &item.Body, &item.Handle, &item.CreatedAt,
			&item.UserID, &item.ReplyTo); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var next any
	if hasMore && len(items) > 0 {
		next = items[len(items)-1].ID
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next, "count": len(items),
	})
}

// mentionPattern 抓取评论正文里的 @handle（§9.1 互动通知的 @）。
// handle 取值域与注册规则一致：3–32 位字母数字下划线。
var mentionPattern = regexp.MustCompile(`@[a-zA-Z0-9_]{3,32}`)

// mentionedHandles 从正文提取去重后的 handle（最多 10 个，防刷查询）。
func mentionedHandles(body string) []string {
	seen := map[string]struct{}{}
	handles := []string{}
	for _, raw := range mentionPattern.FindAllString(body, -1) {
		handle := raw[1:]
		if _, ok := seen[handle]; ok {
			continue
		}
		seen[handle] = struct{}{}
		handles = append(handles, handle)
		if len(handles) == 10 {
			break
		}
	}
	return handles
}

func (h *Handler) addComment(w http.ResponseWriter, r *http.Request) {
	id, err := contentID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var request struct {
		Body     string `json:"body"`
		ParentID int64  `json:"parent_id"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	body := strings.TrimSpace(request.Body)
	if body == "" || utf8.RuneCountInString(body) > 500 {
		h.fail(w, r, admin.ErrInvalidInput)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())

	// 回复压平到顶层：parent 必须是同内容的可见评论；
	// 回复「回复」时挂到同一线程根下，reply_to 记录被回复人（§6.5 回复 @某人）。
	var parentID, replyToUser int64
	if request.ParentID > 0 {
		var parentParent int64
		err = h.db.QueryRow(r.Context(), `
			SELECT COALESCE(c.parent_id, 0), c.user_id
			FROM comments c
			WHERE c.id = $1 AND c.content_id = $2 AND c.status = 'visible'`,
			request.ParentID, id).Scan(&parentParent, &replyToUser)
		if err == pgx.ErrNoRows {
			httpserver.Error(w, http.StatusBadRequest, "invalid_input", "回复的评论不存在",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		// 回复「回复」时压平到同一线程根；reply_to 始终是被回复人。
		parentID = request.ParentID
		if parentParent > 0 {
			parentID = parentParent
		}
	}

	var commentID int64
	var createdAt time.Time
	if err := h.db.QueryRow(r.Context(), `
		INSERT INTO comments (content_id, user_id, body, parent_id, reply_to_user_id)
		VALUES ($1, $2, $3, NULLIF($4, 0), NULLIF($5, 0))
		RETURNING id, created_at`, id, principal.UserID, body, parentID, replyToUser).
		Scan(&commentID, &createdAt); err != nil {
		h.fail(w, r, err)
		return
	}

	h.notifyComment(r.Context(), id, principal, body)
	if parentID > 0 {
		h.notifyCommentReply(r.Context(), id, principal, parentID, replyToUser, body)
	}
	h.notifyMentions(r.Context(), id, principal, body)

	httpserver.JSON(w, http.StatusCreated, Comment{
		ID: commentID, Body: body, Handle: principal.Handle,
		CreatedAt: createdAt, UserID: principal.UserID,
	})
}

// notifyCommentReply 通知被回复人「有人回复了你的评论」。
// 内容作者已经由 notifyComment 覆盖，回复对象是自己则不打扰。
func (h *Handler) notifyCommentReply(ctx context.Context, contentID int64,
	actor auth.Principal, rootID, replyToUser int64, body string) {
	if replyToUser == 0 || replyToUser == actor.UserID {
		return
	}
	contentAuthor, title, err := h.contentOwner(ctx, contentID)
	if err != nil || replyToUser == contentAuthor {
		return
	}
	h.dispatch(ctx, notifications.Message{
		UserID:  replyToUser,
		Kind:    notifications.KindInteraction,
		ActorID: actor.UserID,
		Title:   fmt.Sprintf("@%s 回复了你的评论", actor.Handle),
		Body:    fmt.Sprintf("《%s》：%s", title, excerpt(body, 40)),
	})
}

// notifyMentions 给正文里 @ 到的用户发互动通知；
// 内容作者与被回复人已由其他通知覆盖，不重复打扰。
func (h *Handler) notifyMentions(ctx context.Context, contentID int64,
	actor auth.Principal, body string) {
	handles := mentionedHandles(body)
	if len(handles) == 0 {
		return
	}
	// 评论里 @34 与 @000034 指向同一个人：统一补零后再查。
	for i, handle := range handles {
		handles[i] = nightid.Normalize(handle)
	}
	rows, err := h.db.Query(ctx, `
		SELECT id FROM users
		WHERE handle = ANY($1) AND status <> 'banned'`, handles)
	if err != nil {
		h.log.Warn("comment mentions lookup failed", "error", err)
		return
	}
	defer rows.Close()
	mentioned := map[int64]struct{}{}
	for rows.Next() {
		var mentionedID int64
		if rows.Scan(&mentionedID) == nil {
			mentioned[mentionedID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return
	}
	contentAuthor, title, err := h.contentOwner(ctx, contentID)
	if err != nil {
		return
	}
	for userID := range mentioned {
		if userID == actor.UserID || userID == contentAuthor {
			continue
		}
		h.dispatch(ctx, notifications.Message{
			UserID:  userID,
			Kind:    notifications.KindInteraction,
			ActorID: actor.UserID,
			Title:   fmt.Sprintf("@%s 在评论中提到了你", actor.Handle),
			Body:    fmt.Sprintf("《%s》：%s", title, excerpt(body, 40)),
		})
	}
}

// excerpt 截断正文做通知摘要。
func excerpt(body string, max int) string {
	runes := []rune(body)
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// ── 用户举报（§9.5，24 小时并单）─────────────────────────────────

func (h *Handler) submitReport(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ObjectType string `json:"object_type"`
		ObjectID   int64  `json:"object_id"`
		Reason     string `json:"reason"`
		Detail     string `json:"detail"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	result, err := h.reports.SubmitReport(r.Context(), principal.UserID,
		request.ObjectType, request.ObjectID, request.Reason, request.Detail)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, result)
}

// ── 互动通知（§9.1 互动分组）────────────────────────────────────

func (h *Handler) notifyReaction(ctx context.Context, contentID int64, actor auth.Principal, kind string) {
	authorID, title, err := h.contentOwner(ctx, contentID)
	if err != nil || authorID == actor.UserID {
		return
	}
	label := "赞了你的作品"
	if kind == "favorite" {
		label = "收藏了你的作品"
	}
	h.dispatch(ctx, notifications.Message{
		UserID:       authorID,
		Kind:         notifications.KindInteraction,
		ActorID:      actor.UserID,
		Title:        fmt.Sprintf("@%s %s", actor.Handle, label),
		Body:         fmt.Sprintf("《%s》", title),
		DedupeKey:    fmt.Sprintf("%s:%d:%d", kind, contentID, actor.UserID),
		DedupeWindow: 12 * time.Hour,
	})
}

func (h *Handler) notifyComment(ctx context.Context, contentID int64, actor auth.Principal, body string) {
	authorID, title, err := h.contentOwner(ctx, contentID)
	if err != nil || authorID == actor.UserID {
		return
	}
	h.dispatch(ctx, notifications.Message{
		UserID:  authorID,
		Kind:    notifications.KindInteraction,
		ActorID: actor.UserID,
		Title:   fmt.Sprintf("@%s 评论了你的作品", actor.Handle),
		Body:    fmt.Sprintf("《%s》：%s", title, excerpt(body, 40)),
	})
}

func (h *Handler) contentOwner(ctx context.Context, contentID int64) (int64, string, error) {
	var authorID int64
	var title string
	err := h.db.QueryRow(ctx,
		`SELECT author_id, title FROM contents WHERE id = $1`, contentID).Scan(&authorID, &title)
	if title == "" {
		title = "无标题作品"
	}
	return authorID, title, err
}

// dispatch 通知是尽力而为：写失败不能让用户的重度操作（点赞/评论）跟着失败。
func (h *Handler) dispatch(ctx context.Context, msg notifications.Message) {
	written, err := notifications.Dispatch(ctx, h.db, msg)
	if err != nil {
		h.log.Warn("notification dispatch failed",
			"error", err, "user_id", msg.UserID, "kind", msg.Kind)
		return
	}
	if !written {
		h.log.Debug("notification suppressed by preferences",
			"user_id", msg.UserID, "kind", msg.Kind)
	}
}
