package content

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
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

// Handler exposes content endpoints; write operations require both auth and CSRF.
type Handler struct {
	svc  *Service
	auth *auth.Handler
	log  *slog.Logger
	// 图集上传需要对象存储；S3 在 cmd/api 里是可选启动的，由 SetImageStore 注入。
	imageStore  *objectstore.Store
	imageBucket string
}

func NewHandler(svc *Service, authHandler *auth.Handler, log *slog.Logger) *Handler {
	return &Handler{svc: svc, auth: authHandler, log: log}
}

func (h *Handler) guarded(next http.HandlerFunc) http.Handler {
	return h.auth.RequireAuth(h.auth.RequireCSRF(next))
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/contents", h.guarded(h.create))
	mux.Handle("PATCH /api/v1/contents/{id}", h.guarded(h.update))
	// 作品管理（§11.1）：已发布内容改标题 / 简介 / 可见性，以及软删除。
	mux.Handle("PATCH /api/v1/contents/{id}/manage", h.guarded(h.manage))
	mux.Handle("DELETE /api/v1/contents/{id}", h.guarded(h.remove))
	// 草稿箱批量删除（§11.1 列表型页面的批量操作）。
	mux.Handle("POST /api/v1/contents/batch-delete", h.guarded(h.batchRemove))
	mux.Handle("POST /api/v1/contents/{id}/publish", h.guarded(h.publish))
	mux.Handle("GET /api/v1/contents/mine", h.auth.RequireAuth(http.HandlerFunc(h.mine)))
	// 「我」页面（UI 规范 §10）：计数区 + 作品/喜欢/收藏三分区。
	mux.Handle("GET /api/v1/me/counts", h.auth.RequireAuth(http.HandlerFunc(h.myCounts)))
	mux.Handle("GET /api/v1/me/likes", h.auth.RequireAuth(http.HandlerFunc(h.myLikes)))
	mux.Handle("GET /api/v1/me/favorites", h.auth.RequireAuth(http.HandlerFunc(h.myFavorites)))
	// 创作者中心数据看板（§11.1）：7 / 30 天趋势 + 周期对比 + 热门作品。
	mux.Handle("GET /api/v1/me/analytics", h.auth.RequireAuth(http.HandlerFunc(h.myAnalytics)))
	mux.Handle("GET /api/v1/contents/{id}", h.auth.RequireAuth(http.HandlerFunc(h.get)))
	// 申诉（§9.4）：仅作者本人，写入需 CSRF。
	mux.Handle("POST /api/v1/contents/{id}/appeal", h.guarded(h.appeal))
	mux.Handle("GET /api/v1/contents/{id}/appeals", h.auth.RequireAuth(http.HandlerFunc(h.appeals)))
	mux.Handle("GET /api/v1/appeals/mine", h.auth.RequireAuth(http.HandlerFunc(h.myAppeals)))
	mux.HandleFunc("GET /api/v1/c/{slug}", h.public)
	// 发布页字段规格（§8.1）：候选 / 自建 / 关联话题。
	h.registerTopicRoutes(mux)
	// 作者侧图集（§8.1 图集发布）：上传 / 列表 / 排序 / 删除。
	h.registerGalleryRoutes(mux)
	// 作者侧封面选择（§8.1）：版本化 poster 写入与读取。
	h.registerCoverRoutes(mux)
}

type createRequest struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var request createRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	content, err := h.svc.CreateDraft(r.Context(), principal.UserID, CreateDraftInput{
		Type: request.Type, Title: request.Title, Description: request.Description,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, content)
}

type updateRequest struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Visibility  string          `json:"visibility"`
	POI         json.RawMessage `json:"poi"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var request updateRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	content, err := h.svc.UpdateDraft(r.Context(), id, principal.UserID, UpdateDraftInput{
		Title: request.Title, Description: request.Description,
		Visibility: request.Visibility, POI: request.POI,
		ScheduledAt: request.ScheduledAt,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, content)
}

type publishRequest struct {
	Visibility  string          `json:"visibility"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
	POI         json.RawMessage `json:"poi"`
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	var request publishRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	content, err := h.svc.Publish(r.Context(), id, principal.UserID, PublishInput{
		Visibility: request.Visibility, ScheduledAt: request.ScheduledAt, POI: request.POI,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, content)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	content, err := h.svc.Owned(r.Context(), id, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, content)
}

func (h *Handler) mine(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	state := r.URL.Query().Get("state")
	if state != "" && state != "draft" && state != "published" && state != "scheduled" {
		h.writeError(w, r, fmt.Errorf("%w：state 只能是 draft / published / scheduled", ErrInvalidInput))
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	// order 控制排序方向：desc（默认，最新在前）| asc（最早在前）。
	order := r.URL.Query().Get("order")
	if order != "asc" {
		order = "desc"
	}
	items, hasMore, err := h.svc.ListMine(r.Context(), principal.UserID, limit, offset, state, order)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.attachMediaStatus(r.Context(), items)
	views := h.cardViews(r.Context(), contentIDs(items))
	for i := range items {
		view := views[items[i].ID]
		items[i].PosterURL = view.PosterURL
		items[i].LikeCount = view.LikeCount
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "count": len(items), "has_more": hasMore, "offset": offset,
	})
}

// attachMediaStatus 为「我的作品」里的视频补充转码状态（§10 处理中徽标）。
// 一次批量查询；失败时留空，前端按无徽标呈现，不影响列表。
func (h *Handler) attachMediaStatus(ctx context.Context, items []Content) {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if item.Type == "video" {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	statuses, err := h.svc.TranscodeStatuses(ctx, ids)
	if err != nil {
		h.log.Warn("media status lookup failed", "error", err)
		return
	}
	for i := range items {
		if items[i].Type != "video" {
			continue
		}
		// 无转码记录 = 原始上传尚未就绪，也算处理中。
		items[i].MediaStatus = statuses[items[i].ID]
		if items[i].MediaStatus == "" {
			items[i].MediaStatus = "processing"
		}
	}
}

func contentIDs(items []Content) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

// cardView 是列表卡片的可展示字段。
type cardView struct {
	PosterURL string
	LikeCount int64
}

// cardViews 一次批量补齐封面与点赞数。driver=s3 走签名 URL，
// local_temp 的 demo 路径直接用（与 interactions.attachPosters 同口径，
// 否则会把 /demo/c1.jpeg 当 S3 key 签成 404）。查询失败只记日志，
// 卡片回退到类型占位，不影响列表本身。
func (h *Handler) cardViews(ctx context.Context, ids []int64) map[int64]cardView {
	views := make(map[int64]cardView, len(ids))
	metas, err := h.svc.repo.CardMeta(ctx, ids)
	if err != nil {
		h.log.Warn("card meta lookup failed", "error", err)
		return views
	}
	for id, meta := range metas {
		view := cardView{LikeCount: meta.Likes}
		driver, key, ok := strings.Cut(meta.Cover, ":")
		if ok && key != "" {
			if driver == "s3" && h.imageStore != nil {
				if url, err := h.imageStore.MediaURL(ctx, key); err == nil {
					view.PosterURL = url
				}
			} else if driver != "s3" {
				view.PosterURL = key
			}
		}
		views[id] = view
	}
	return views
}

// myCounts 提供「我」页面的计数区（获赞 / 关注 / 粉丝）。
func (h *Handler) myCounts(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	counts, err := h.svc.UserCounts(r.Context(), principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, counts)
}

// myReactions 是「喜欢 / 收藏」分区的共用实现：附带作者 handle，
// 前端据此生成可点击的作者链接。
func (h *Handler) myReactions(w http.ResponseWriter, r *http.Request, kind string) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.svc.ReactedContents(r.Context(), principal.UserID, kind, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	views := h.cardViews(r.Context(), ids)
	for i := range items {
		view := views[items[i].ID]
		items[i].PosterURL = view.PosterURL
		items[i].LikeCount = view.LikeCount
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) myLikes(w http.ResponseWriter, r *http.Request) {
	h.myReactions(w, r, "like")
}

func (h *Handler) myFavorites(w http.ResponseWriter, r *http.Request) {
	h.myReactions(w, r, "favorite")
}

// public is the deep-link/SEO API: private or not-yet-approved content returns 404.
func (h *Handler) public(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		h.writeError(w, r, ErrNotFound)
		return
	}
	content, err := h.svc.GetBySlug(r.Context(), slug)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	viewerID := int64(0)
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerID = principal.UserID
	}
	visible, err := h.svc.Visible(r.Context(), content, viewerID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !visible {
		h.writeError(w, r, ErrNotFound)
		return
	}
	httpserver.JSON(w, http.StatusOK, content)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_id", "invalid content id",
			httpserver.RequestIDFromContext(r.Context()))
		return 0, false
	}
	return id, true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	message := "unexpected server error"
	switch {
	case errors.Is(err, httpserver.ErrInvalidJSON), errors.Is(err, ErrInvalidInput),
		errors.Is(err, ErrInvalidType), errors.Is(err, ErrTitleTooLong),
		errors.Is(err, ErrBodyTooLong), errors.Is(err, ErrVisibility), errors.Is(err, ErrScheduleSoon),
		errors.Is(err, ErrTopicName), errors.Is(err, ErrTopicLimit), errors.Is(err, ErrTopicUnavailable),
		errors.Is(err, ErrGallerySize), errors.Is(err, ErrGalleryLimit), errors.Is(err, ErrGalleryType),
		errors.Is(err, ErrBadImage), errors.Is(err, ErrGalleryOrder),
		errors.Is(err, ErrCoverType):
		status, code, message = http.StatusBadRequest, "invalid_input", err.Error()
	case errors.Is(err, ErrNotDraft):
		status, code, message = http.StatusConflict, "not_draft", err.Error()
	case errors.Is(err, ErrAppealExists):
		status, code, message = http.StatusConflict, "appeal_exists", "该作品已有待处理申诉"
	case errors.Is(err, ErrNotAppealable):
		status, code, message = http.StatusConflict, "not_appealable", "当前状态不可申诉"
	case errors.Is(err, ErrAppealReason):
		status, code, message = http.StatusBadRequest, "invalid_appeal_reason",
			"申诉理由需 5-500 字"
	case errors.Is(err, ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", err.Error()
	case errors.Is(err, ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "content not found"
	case errors.Is(err, ErrTopicNotFound):
		status, code, message = http.StatusNotFound, "not_found", "话题不存在"
	case errors.Is(err, ErrGalleryAsset):
		status, code, message = http.StatusNotFound, "not_found", "图片不存在"
	case errors.Is(err, ErrNoCover):
		status, code, message = http.StatusNotFound, "not_found", "该作品还没有封面"
	case errors.Is(err, ErrNoManifest):
		status, code, message = http.StatusConflict, "no_manifest", err.Error()
	case errors.Is(err, ErrCoverState):
		status, code, message = http.StatusConflict, "cover_state", err.Error()
	case errors.Is(err, ErrStorageDown):
		status, code, message = http.StatusServiceUnavailable, "storage_unavailable", err.Error()
	}
	if status == http.StatusInternalServerError {
		h.log.Error("content error", "error", err, "path", r.URL.Path)
	}
	httpserver.Error(w, status, code, message, httpserver.RequestIDFromContext(r.Context()))
}
