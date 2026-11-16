package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/content"
	"github.com/yejie/voidxotv/internal/modules/notifications"
	"github.com/yejie/voidxotv/internal/modules/playback"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

type Handler struct {
	repo       *Repository
	playback   *playback.Handler
	store      *objectstore.Store
	content    *content.Service
	auth       *auth.Handler
	publicRead bool
	// ranking 为 "rule"（默认，§9.3 可解释规则）或 "newest"（回退）。
	ranking       string
	candidateDays int
	log           *slog.Logger
}

func NewHandler(repo *Repository, playbackHandler *playback.Handler, store *objectstore.Store,
	contentService *content.Service, authHandler *auth.Handler, publicRead bool,
	ranking string, candidateDays int, log *slog.Logger) *Handler {
	if ranking != "newest" {
		ranking = "rule"
	}
	if candidateDays <= 0 {
		candidateDays = 30
	}
	return &Handler{
		repo: repo, playback: playbackHandler, store: store, content: contentService,
		auth: authHandler, publicRead: publicRead, ranking: ranking,
		candidateDays: candidateDays, log: log,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/feed", h.list)
	mux.HandleFunc("GET /api/v1/feed/{slug}", h.detail)
	// 创作者主页与话题页（公开、可索引，供 Delivery 的 /creator/** /topic/** 落地）。
	mux.HandleFunc("GET /api/v1/creators/{handle}", h.creator)
	mux.HandleFunc("GET /api/v1/topics/{slug}", h.topic)
	// 关注关系（§9.1）：读状态免 CSRF，写必须带 CSRF；关注流需要登录态。
	mux.Handle("GET /api/v1/creators/{handle}/follow", h.auth.RequireAuth(http.HandlerFunc(h.followState)))
	mux.Handle("POST /api/v1/creators/{handle}/follow",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.setFollow))))
	mux.Handle("GET /api/v1/me/following", h.auth.RequireAuth(http.HandlerFunc(h.following)))
	// 计数区点进来的三类列表（§10）：关注 / 粉丝 / 赞过我。
	mux.Handle("GET /api/v1/me/relations", h.auth.RequireAuth(http.HandlerFunc(h.relations)))
}

func (h *Handler) creator(w http.ResponseWriter, r *http.Request) {
	handle := r.PathValue("handle")
	profile, err := h.repo.Creator(r.Context(), handle)
	if err != nil {
		h.notFound(w, r)
		return
	}
	if profile.AvatarKey != "" && h.store != nil {
		// 公开读关闭时必须签名，否则头像会 403（与图集同一条规则）。
		if url, err := h.store.MediaURL(r.Context(), profile.AvatarKey); err == nil {
			profile.AvatarURL = url
		}
	}
	rows, err := h.repo.ListByHandle(r.Context(), handle, 0, 24)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]Item, 0, len(rows))
	for _, entry := range rows {
		item, err := h.compose(r.Context(), entry, false)
		if err != nil {
			// 缺产物的条目直接跳过（§3.2 媒体可用性），不影响整页。
			h.log.Warn("feed: skip item", "content_id", entry.ID, "error", err)
			continue
		}
		items = append(items, item)
	}
	h.attachAvatars(r.Context(), items)
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"profile": profile, "items": items, "count": len(items),
	})
}

func (h *Handler) topic(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	profile, err := h.repo.Topic(r.Context(), slug)
	if err != nil {
		h.notFound(w, r)
		return
	}
	// 自建话题待审或已下线时，作品关联保留但不再公开呈现（§4.7）。
	if profile.Status != "available" {
		w.Header().Set("X-Robots-Tag", "noindex")
		httpserver.JSON(w, http.StatusOK, map[string]any{
			"profile": profile, "items": []Item{}, "count": 0,
			"note": "该话题当前未公开",
		})
		return
	}
	rows, err := h.repo.ListByTopic(r.Context(), slug, 0, 24)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]Item, 0, len(rows))
	for _, entry := range rows {
		item, err := h.compose(r.Context(), entry, false)
		if err != nil {
			// 缺产物的条目直接跳过（§3.2 媒体可用性），不影响整页。
			h.log.Warn("feed: skip item", "content_id", entry.ID, "error", err)
			continue
		}
		items = append(items, item)
	}
	h.attachAvatars(r.Context(), items)
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"profile": profile, "items": items, "count": len(items),
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 30 {
		limit = 12
	}
	rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	// channel=follow 是「关注流」：只出我关注作者的作品，游客按未登录处理。
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))

	// 关注关系只影响已登录用户；游客不获得 follow 加分（§9.3 隐私要求）。
	viewerID := int64(0)
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerID = principal.UserID
	}

	var rows []row
	var err error
	if channel == "follow" {
		if viewerID == 0 {
			httpserver.Error(w, http.StatusUnauthorized, "login_required", "登录后可查看关注流",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		cursor, _ := strconv.ParseInt(rawCursor, 10, 64)
		if cursor < 0 {
			cursor = 0
		}
		rows, err = h.repo.ListFollowing(r.Context(), viewerID, cursor, limit)
	} else if h.ranking == "newest" {
		cursor, _ := strconv.ParseInt(rawCursor, 10, 64)
		if cursor < 0 {
			cursor = 0
		}
		rows, err = h.repo.List(r.Context(), cursor, limit)
	} else {
		score, id := parseRankedCursor(rawCursor)
		rows, err = h.repo.ListRanked(r.Context(), viewerID, score, id, limit, h.candidateDays)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	items := make([]Item, 0, len(rows))
	for _, entry := range rows {
		item, err := h.compose(r.Context(), entry, false)
		if err != nil {
			// A single broken asset must not blank the whole stream.
			h.log.Warn("feed: skip item", "content_id", entry.ID, "error", err)
			continue
		}
		items = append(items, item)
	}

	// 游标必须来自最后一条「数据行」而非最后一条成功组装的条目，
	// 否则跳过的条目会导致重复或漏条。
	var next any
	if len(rows) == limit && len(rows) > 0 {
		last := rows[len(rows)-1]
		if h.ranking == "newest" {
			next = last.ID
		} else {
			next = formatRankedCursor(last.Score, last.ID)
		}
	}
	h.attachAvatars(r.Context(), items)
	h.applyViewerMarks(r.Context(), viewerID, items)
	w.Header().Set("Cache-Control", "private, no-store")
	httpserver.JSON(w, http.StatusOK, Response{Items: items, NextCursor: next, Count: len(items)})
}

// parseRankedCursor accepts the opaque "score:id" token; malformed input is
// treated as the first page rather than an error.
func parseRankedCursor(raw string) (float64, int64) {
	if raw == "" {
		return 0, 0
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	score, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || score < 0 {
		return 0, 0
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 0 {
		return 0, 0
	}
	return score, id
}

func formatRankedCursor(score float64, id int64) string {
	// 用最短往返表示（'g'/-1）：固定 6 位小数会四舍五入后大于真实分数，
	// 使边界行在下一页被重复包含。
	return strconv.FormatFloat(score, 'g', -1, 64) + ":" + strconv.FormatInt(id, 10)
}

// detail is the deep-link/SEO API. Full playlists are only built for the item
// the visitor actually opened, keeping list responses cheap.
func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	entry, err := h.content.GetBySlug(r.Context(), slug)
	if err != nil {
		h.notFound(w, r)
		return
	}
	viewerID := int64(0)
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerID = principal.UserID
	}
	visible, err := h.content.Visible(r.Context(), entry, viewerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !visible {
		h.notFound(w, r)
		return
	}
	handle, err := h.repo.HandleByID(r.Context(), entry.AuthorID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	item, err := h.compose(r.Context(), row{
		ID: entry.ID, Slug: entry.Slug, Type: entry.Type, Title: entry.Title,
		Description: entry.Description, AuthorID: entry.AuthorID, Handle: handle,
		PublishedAt: entry.PublishedAt, DurationMS: entry.DurationMS,
		Origin: entry.Origin, SourceURL: entry.SourceURL,
	}, true)
	if err != nil {
		// 已发布但产物缺失的内容对外等同不存在，不暴露「存在但坏了」。
		if errors.Is(err, ErrNoPlayableSource) {
			h.notFound(w, r)
			return
		}
		h.fail(w, r, err)
		return
	}
	single := []Item{item}
	h.attachAvatars(r.Context(), single)
	h.applyViewerMarks(r.Context(), viewerID, single)
	w.Header().Set("Cache-Control", "private, no-store")
	httpserver.JSON(w, http.StatusOK, single[0])
}

// Compose builds the public item for a listing row. Search and category pages
// reuse it so poster/HLS/HEVC rules never diverge between surfaces.
func (h *Handler) Compose(ctx context.Context, entry Entry, full bool) (Item, error) {
	return h.compose(ctx, entry, full)
}

func (h *Handler) compose(ctx context.Context, entry row, full bool) (Item, error) {
	item := Item{
		ID: entry.ID, Slug: entry.Slug, Type: entry.Type,
		Title: entry.Title, Description: entry.Description,
		Author:      Author{ID: entry.AuthorID, Handle: entry.Handle, DisplayName: entry.DisplayName},
		PublishedAt: entry.PublishedAt,
		Origin:      entry.Origin, SourceURL: entry.SourceURL,
	}
	// 详情才带位置信息（§8 发布设置）：列表不查，省一次 join；图集与视频都要。
	if full {
		var raw []byte
		if err := h.repo.DB().QueryRow(ctx,
			`SELECT poi FROM contents WHERE id = $1`, entry.ID).Scan(&raw); err == nil &&
			len(raw) > 0 && string(raw) != "{}" && string(raw) != "null" {
			var poi POI
			if json.Unmarshal(raw, &poi) == nil && poi.Name != "" {
				item.POI = &poi
			}
		}
	}

	// 真实互动计数（§6.2 角标口径）：查询失败只记日志、保持零值，
	// 计数缺失不应让整条内容从流里消失。
	if stats, err := h.repo.InteractionStats(ctx, entry.ID); err != nil {
		h.log.Warn("feed: interaction stats", "content_id", entry.ID, "error", err)
	} else {
		item.Stats = stats
	}

	// Image galleries have no transcode manifest: their first image is the cover.
	if entry.Type == "image" {
		addresses, err := h.images(ctx, entry.ID)
		if err != nil {
			return item, err
		}
		item.Images = addresses
		item.Media = Media{Mode: "image", Status: "ready"}
		if len(addresses) > 0 {
			item.Media.PosterURL = addresses[0]
		}
		return item, nil
	}

	contentRow := entry.asContent()
	signed := !h.publicRead || contentRow.Visibility != "public"
	var result playback.Playback
	var err error
	if full {
		result, err = h.playback.Build(ctx, contentRow, signed)
	} else {
		result, err = h.playback.Summary(ctx, contentRow, signed)
	}
	if err != nil {
		return item, err
	}
	// 沉浸流资格包含「媒体可用性」（§3.2）：没有可播放产物的内容不得出现在
	// feed / 搜索 / 分类页，否则用户会拿到一个无法播放的条目。
	if result.Mode == "none" ||
		(result.MediaURL == "" && result.MasterPlaylist == "" && len(result.Playlists) == 0) {
		return item, ErrNoPlayableSource
	}
	item.Media = Media{
		Mode: result.Mode, PosterURL: result.PosterURL, MediaURL: result.MediaURL,
		DurationMS: result.DurationMS, Signed: result.Signed, ExpiresAt: result.ExpiresAt,
		MasterPlaylist: result.MasterPlaylist, Playlists: result.Playlists,
		Status: result.Status,
	}
	// HEVC 只作为可选源下发，默认播放仍用 MediaURL（§5.1 H.264 必选回退）。
	for _, file := range result.Files {
		if file.Kind == "mp4_hevc" {
			item.Media.HEVCURL = file.URL
			break
		}
	}

	return item, nil
}

// attachAvatars 一次查询补全这批条目的作者头像（见 repo.Avatars）。
// 失败只记日志：头像缺失不应让整条流失败。
func (h *Handler) attachAvatars(ctx context.Context, items []Item) {
	if h.store == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.Author.ID == 0 {
			continue
		}
		if _, ok := seen[item.Author.ID]; ok {
			continue
		}
		seen[item.Author.ID] = struct{}{}
		ids = append(ids, item.Author.ID)
	}
	keys, err := h.repo.Avatars(ctx, ids)
	if err != nil {
		h.log.Warn("feed: author avatars", "error", err)
		return
	}
	for i := range items {
		key := keys[items[i].Author.ID]
		if key == "" {
			continue
		}
		if url, err := h.store.MediaURL(ctx, key); err == nil {
			items[i].Author.AvatarURL = url
		}
	}
}

// applyViewerMarks 把查看者自己的点赞 / 收藏标记写回条目（§6.2 操作栏回显）。
// 只在带登录态的接口（feed 列表 / 详情）调用：creator / topic 是公开缓存页，
// 混入查看者状态会被 CDN 缓存串号。失败只记日志，回退到「未点亮」。
func (h *Handler) applyViewerMarks(ctx context.Context, viewerID int64, items []Item) {
	if viewerID <= 0 || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	marks, err := h.repo.ViewerReactions(ctx, viewerID, ids)
	if err != nil {
		h.log.Warn("feed: viewer reactions", "error", err)
		return
	}
	for i := range items {
		if mark, ok := marks[items[i].ID]; ok {
			items[i].Liked = mark.Liked
			items[i].Favorited = mark.Favorited
		}
	}
}

func (h *Handler) images(ctx context.Context, contentID int64) ([]string, error) {
	addresses, err := h.repo.Gallery(ctx, contentID)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		driver, key, found := strings.Cut(address, ":")
		if !found || key == "" {
			continue
		}
		if driver == "s3" {
			// 公开读关闭时 PublicURL 会 403；与播放地址同口径改为短时签名 GET。
			// 生产开启公共读后两者都是稳定 CDN 路径，与图片 sitemap 一致。
			resolved, err := h.store.MediaURL(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("feed: gallery url: %w", err)
			}
			key = resolved
		}
		// driver=local_temp keys are dev-only static demo paths (e.g. /demo/c1.jpeg).
		result = append(result, key)
	}
	return result, nil
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	httpserver.Error(w, http.StatusNotFound, "not_found", "content not found",
		httpserver.RequestIDFromContext(r.Context()))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("feed error", "error", err, "path", r.URL.Path)
	httpserver.Error(w, http.StatusInternalServerError, "internal_error", "unexpected server error",
		httpserver.RequestIDFromContext(r.Context()))
}

// ── 关注关系（§9.1）──────────────────────────────────────────────

// following 返回我关注的全部 handle，信息流首屏据此水合「已关注」状态。
func (h *Handler) following(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	handles, err := h.repo.FollowingHandles(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"handles": handles, "count": len(handles)})
}

func (h *Handler) followState(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	handle := r.PathValue("handle")
	targetID, err := h.repo.FollowTargetID(r.Context(), handle)
	if err != nil {
		h.notFound(w, r)
		return
	}
	following, err := h.repo.IsFollowing(r.Context(), principal.UserID, targetID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	count, err := h.repo.CountFollowers(r.Context(), targetID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"handle": handle, "following": following, "follower_count": count,
	})
}

func (h *Handler) setFollow(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	handle := r.PathValue("handle")
	var request struct {
		Value bool `json:"value"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		httpserver.Error(w, http.StatusBadRequest, "invalid_json", "请求体不合法",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	targetID, err := h.repo.FollowTargetID(r.Context(), handle)
	if err != nil {
		h.notFound(w, r)
		return
	}
	if targetID == principal.UserID {
		httpserver.Error(w, http.StatusBadRequest, "self_follow", "不能关注自己",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	changed, err := h.repo.SetFollow(r.Context(), principal.UserID, targetID, request.Value)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	count, err := h.repo.CountFollowers(r.Context(), targetID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// 只有「新产生的关注」才通知，重复关注 / 取消不打扰（§9.1 关注通知）。
	if request.Value && changed {
		written, notifyErr := notifications.Dispatch(r.Context(), h.repo.DB(), notifications.Message{
			UserID:       targetID,
			Kind:         notifications.KindFollow,
			ActorID:      principal.UserID,
			Title:        fmt.Sprintf("@%s 关注了你", principal.Handle),
			Body:         "你可以在关注流里看到他/她的新作品。",
			DedupeKey:    fmt.Sprintf("follow:%d:%d", principal.UserID, targetID),
			DedupeWindow: 7 * 24 * time.Hour,
		})
		if notifyErr != nil {
			h.log.Warn("follow notification failed", "error", notifyErr, "target", targetID)
		} else if !written {
			h.log.Debug("follow notification suppressed", "target", targetID)
		}
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"handle": handle, "following": request.Value, "follower_count": count,
	})
}
