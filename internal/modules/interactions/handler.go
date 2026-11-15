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
	"github.com/yejie/voidxotv/internal/modules/gifs"
	"github.com/yejie/voidxotv/internal/modules/notifications"
	"github.com/yejie/voidxotv/internal/modules/spam"
	"github.com/yejie/voidxotv/internal/modules/stats"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/httpx"
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
	// 评论点赞：登录 + CSRF，与内容 reactions 分开计数。
	mux.Handle("POST /api/v1/comments/{commentID}/like", h.guarded(h.toggleCommentLike))
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
	// 与限流 / 审计同一口径：只认 TCP 对端，受信代理时才取 XFF 最右一跳。
	host := httpx.ClientIP(r)
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

// avatarURL 把头像对象键换成可访问地址。未注入对象存储（本地只起 API 不起 S3）
// 或签名失败时返回空串，前端据此回退到默认用户图标，评论本身不受影响。
func (h *Handler) avatarURL(ctx context.Context, key string) string {
	if h.store == nil || key == "" {
		return ""
	}
	url, err := h.store.MediaURL(ctx, key)
	if err != nil {
		h.log.Warn("interactions: avatar url", "error", err, "key", key)
		return ""
	}
	return url
}

// ── 评论（§6.5 评论抽屉：分页列表、楼中楼回复、@提及通知）──────────

type Comment struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	Handle string `json:"handle"`
	// DisplayName 是展示用昵称（默认「用户 + 夜街号」）；Handle 只用于 @ 提及与定位用户。
	DisplayName string `json:"display_name"`
	// AvatarURL 是可直接访问的头像地址；未设置头像时为空，前端回退到默认图标。
	AvatarURL string    `json:"avatar_url"`
	CreatedAt time.Time `json:"created_at"`
	UserID    int64     `json:"user_id"`
	// ReplyCount 是该顶层评论线程内的可见回复数（仅顶层评论返回）。
	ReplyCount int64 `json:"reply_count"`
	// VerifiedCreator 表示作者是已认证创作者（users.role = 'creator'），前端据此在昵称旁挂标签。
	VerifiedCreator bool `json:"verified_creator"`
	// LikeCount / Liked 是评论点赞数与当前访客是否已赞（匿名恒 false）。
	LikeCount int64 `json:"like_count"`
	Liked     bool  `json:"liked"`
	// avatarKey 只在服务端流转：对外一律给可访问地址，不泄漏对象键。
	avatarKey string
}

type Reply struct {
	ID          int64  `json:"id"`
	Body        string `json:"body"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	// VerifiedCreator 同 Comment：作者是否为已认证创作者。
	VerifiedCreator bool      `json:"verified_creator"`
	CreatedAt       time.Time `json:"created_at"`
	UserID          int64     `json:"user_id"`
	// ReplyTo 是「回复 @某人」的目标作者昵称（回复线程内其他回复时才有）。
	ReplyTo string `json:"reply_to,omitempty"`
	// LikeCount / Liked 同 Comment：楼中楼回复也能点赞。
	LikeCount int64 `json:"like_count"`
	Liked     bool  `json:"liked"`
	avatarKey string
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

	// 当前访客的点赞态按请求即时算，匿名传 0；不进公共缓存口径。
	var viewerID int64
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerID = principal.UserID
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT c.id, c.body, u.handle,
		       COALESCE(NULLIF(btrim(u.display_name), ''), u.handle),
		       COALESCE(u.avatar_key, ''),
		       u.role = 'creator',
		       c.created_at, c.user_id,
		       (SELECT count(*) FROM comment_likes cl WHERE cl.comment_id = c.id),
		       ($4 > 0 AND EXISTS (SELECT 1 FROM comment_likes cl
		           WHERE cl.comment_id = c.id AND cl.user_id = $4)),
		       (SELECT count(*) FROM comments rep
		        WHERE rep.parent_id = c.id AND rep.status = 'visible')
		FROM comments c JOIN users u ON u.id = c.user_id
		WHERE c.content_id = $1 AND c.status = 'visible' AND c.parent_id IS NULL
		  AND ($2 = 0 OR c.id < $2)
		ORDER BY c.id DESC LIMIT $3`, id, cursor, limit+1, viewerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []Comment{}
	for rows.Next() {
		var item Comment
		if err := rows.Scan(&item.ID, &item.Body, &item.Handle, &item.DisplayName,
			&item.avatarKey, &item.VerifiedCreator, &item.CreatedAt, &item.UserID,
			&item.LikeCount, &item.Liked, &item.ReplyCount); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	for i := range items {
		items[i].AvatarURL = h.avatarURL(r.Context(), items[i].avatarKey)
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

	var viewerID int64
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerID = principal.UserID
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT c.id, c.body, u.handle,
		       COALESCE(NULLIF(btrim(u.display_name), ''), u.handle),
		       COALESCE(u.avatar_key, ''),
		       u.role = 'creator',
		       c.created_at, c.user_id,
		       (SELECT count(*) FROM comment_likes cl WHERE cl.comment_id = c.id),
		       ($4 > 0 AND EXISTS (SELECT 1 FROM comment_likes cl
		           WHERE cl.comment_id = c.id AND cl.user_id = $4)),
		       COALESCE(NULLIF(btrim(rt.display_name), ''), rt.handle, '')
		FROM comments c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN users rt ON rt.id = c.reply_to_user_id
		WHERE c.parent_id = $1 AND c.status = 'visible'
		  AND ($2 = 0 OR c.id > $2)
		ORDER BY c.id ASC LIMIT $3`, rootID, cursor, limit+1, viewerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []Reply{}
	for rows.Next() {
		var item Reply
		if err := rows.Scan(&item.ID, &item.Body, &item.Handle, &item.DisplayName,
			&item.avatarKey, &item.VerifiedCreator, &item.CreatedAt, &item.UserID,
			&item.LikeCount, &item.Liked, &item.ReplyTo); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	for i := range items {
		items[i].AvatarURL = h.avatarURL(r.Context(), items[i].avatarKey)
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

// toggleCommentLike 切换当前用户对一条评论的点赞，返回最新状态与总数。
// 与内容 reactions 分开：不改内容计数，也不派发互动通知。
func (h *Handler) toggleCommentLike(w http.ResponseWriter, r *http.Request) {
	commentID, err := strconv.ParseInt(r.PathValue("commentID"), 10, 64)
	if err != nil || commentID <= 0 {
		h.fail(w, r, admin.ErrInvalidInput)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	var visible bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM comments WHERE id = $1 AND status = 'visible')`,
		commentID).Scan(&visible); err != nil {
		h.fail(w, r, err)
		return
	}
	if !visible {
		httpserver.Error(w, http.StatusNotFound, "not_found", "评论不存在",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	tag, err := h.db.Exec(r.Context(),
		`DELETE FROM comment_likes WHERE comment_id = $1 AND user_id = $2`,
		commentID, principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	liked := tag.RowsAffected() == 0
	if liked {
		if _, err := h.db.Exec(r.Context(),
			`INSERT INTO comment_likes (comment_id, user_id) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, commentID, principal.UserID); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	var likeCount int64
	if err := h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM comment_likes WHERE comment_id = $1`, commentID).Scan(&likeCount); err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"liked": liked, "like_count": likeCount,
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

// maxCommentRunes 是评论 / 回复的可见字符上限：标点与单个表情各算 1，
// GIF 标记整体算 1（用户看到的就是一张图），不让长标记吃掉额度。
const maxCommentRunes = 140

func commentLength(body string) int {
	markers := gifs.Markers(body)
	if len(markers) == 0 {
		return utf8.RuneCountInString(body)
	}
	plain := body
	for _, raw := range markers {
		plain = strings.ReplaceAll(plain, gifs.Marker(raw), "")
	}
	return utf8.RuneCountInString(strings.TrimSpace(plain)) + len(markers)
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
	if body == "" || commentLength(body) > maxCommentRunes {
		h.fail(w, r, fmt.Errorf("%w: 评论最多 %d 字", admin.ErrInvalidInput, maxCommentRunes))
		return
	}
	// GIF 标记里的地址只能是集成过的图源（apihz / klipy）：评论正文会被渲染成 <img>，
	// 放开任意 https 等于给评论一条嵌外部图片（追踪像素）的口子。
	for _, raw := range gifs.Markers(body) {
		if !gifs.ValidGifURL(raw) {
			h.fail(w, r, fmt.Errorf("%w: 表情包地址无效，请重新选择", admin.ErrInvalidInput))
			return
		}
	}
	// 表情包标记里的地址不是用户输入的文字，审核前剔除，避免把图源 URL 当正文。
	clean := body
	for _, raw := range gifs.Markers(body) {
		clean = strings.ReplaceAll(clean, gifs.Marker(raw), "")
	}
	if spam.Guard(w, r, spam.Field{Label: "评论", Value: clean}) {
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

	// 通知里只给可读文本：把 GIF 标记换成 [GIF]，否则通知摘要会露出 [[gif:https://…]]。
	plain := gifs.StripMarkers(body)
	if plain == "" {
		plain = "[GIF]"
	}
	h.notifyComment(r.Context(), id, principal, plain)
	if parentID > 0 {
		h.notifyCommentReply(r.Context(), id, principal, parentID, replyToUser, plain)
	}
	h.notifyMentions(r.Context(), id, principal, plain)

	// 作者信息此时还没进列表，单独回填一次：客户端用返回值替换乐观插入的占位条目。
	// 认证标记也要一起回，否则刚发的评论会少一个标签、刷新后才补上。
	var displayName, avatarKey string
	var verified bool
	if err := h.db.QueryRow(r.Context(), `
		SELECT COALESCE(NULLIF(btrim(display_name), ''), handle), COALESCE(avatar_key, ''),
		       role = 'creator'
		FROM users WHERE id = $1`, principal.UserID).Scan(
		&displayName, &avatarKey, &verified); err != nil {
		h.log.Warn("interactions: comment author", "error", err, "user_id", principal.UserID)
		displayName = principal.Handle
	}

	httpserver.JSON(w, http.StatusCreated, Comment{
		ID: commentID, Body: body, Handle: principal.Handle,
		DisplayName: displayName, AvatarURL: h.avatarURL(r.Context(), avatarKey),
		VerifiedCreator: verified,
		CreatedAt:       createdAt, UserID: principal.UserID,
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
	if spam.Guard(w, r, spam.Field{Label: "举报说明", Value: request.Detail}) {
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
