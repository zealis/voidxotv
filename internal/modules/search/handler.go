package search

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/feed"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 搜索防滥用基线（§9.2 固定规则）：最小 2 字符、上限 64 字符、5 秒查询间隔。
const (
	MinQueryRunes   = 2
	MaxQueryRunes   = 64
	SearchCooldown  = 5 * time.Second
	defaultPageSize = 12
	maxPageSize     = 30
)

var (
	errQueryTooShort = errors.New("query is shorter than 2 characters")
	errQueryTooLong  = errors.New("query is longer than 64 characters")
	errQueryChars    = errors.New("query contains unsupported control characters")
	errBadFilter     = errors.New("unsupported filter value")
	errCategory      = errors.New("unknown category")
	errRateLimited   = errors.New("search rate limited")
)

type Handler struct {
	repo       *Repository
	feed       *feed.Handler
	auth       *auth.Handler
	redis      *goredis.Client
	publicRead bool
	log        *slog.Logger
}

func NewHandler(repo *Repository, feedHandler *feed.Handler, authHandler *auth.Handler,
	redisClient *goredis.Client, publicRead bool, log *slog.Logger) *Handler {
	return &Handler{
		repo: repo, feed: feedHandler, auth: authHandler, redis: redisClient,
		publicRead: publicRead, log: log,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// 搜索须登录后使用（§9.2；游客搜索是否放开见 §17 待决策）。
	mux.Handle("GET /api/v1/search", h.auth.RequireAuth(http.HandlerFunc(h.search)))
	mux.Handle("GET /api/v1/search/hot", h.auth.RequireAuth(http.HandlerFunc(h.hot)))
	// 分类列表与详情是公开内容（可索引），供 /category/[slug] 落地页使用。
	mux.HandleFunc("GET /api/v1/categories", h.categories)
	mux.HandleFunc("GET /api/v1/categories/{slug}", h.category)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		h.writeError(w, r, auth.ErrUnauthorized)
		return
	}

	criteria, err := parseCriteria(r, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 分类筛选按 slug 传入（对客户端友好），服务端解析为 id 后再参与查询。
	if slug := strings.TrimSpace(r.URL.Query().Get("category")); slug != "" {
		profile, catErr := h.repo.Category(r.Context(), slug)
		if errors.Is(catErr, ErrNotFound) {
			h.writeError(w, r, errCategory)
			return
		}
		if catErr != nil {
			h.fail(w, r, catErr)
			return
		}
		criteria.CategoryID = profile.ID
	}

	// 固定搜索间隔限流：同一账号 5 秒内只允许一次查询（§9.2）。
	allowed, retryAfter, err := h.acquire(r.Context(), principal.UserID)
	if err != nil {
		// Redis 不可用时按规范 fail-closed：搜索属于可被刷取的读取接口。
		h.log.Error("search: limiter unavailable", "error", err)
		httpserver.Error(w, http.StatusServiceUnavailable, "search_unavailable",
			"search is temporarily unavailable", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		httpserver.Error(w, http.StatusTooManyRequests, "search_rate_limited",
			"search again after the cooldown", httpserver.RequestIDFromContext(r.Context()))
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > maxPageSize {
		limit = defaultPageSize
	}
	score, id := parseCursor(r.URL.Query().Get("cursor"))

	// 多取一条判断下一页，不统计总条数（§9.2）。
	entries, err := h.repo.Search(r.Context(), criteria, likePattern(criteria.Query),
		score, id, limit+1)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}

	items := make([]feed.Item, 0, len(entries))
	for _, entry := range entries {
		item, composeErr := h.feed.Compose(r.Context(), entry, false)
		if composeErr != nil {
			// 单条资产不可用不应让整个搜索结果页失败。
			h.log.Warn("search: skip item", "content_id", entry.ID, "error", composeErr)
			continue
		}
		items = append(items, item)
	}
	h.attachTags(r.Context(), entries, items)

	var next any
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		next = formatCursor(last.Score, last.ID)
	}

	took := time.Since(started)
	filters := map[string]any{"type": criteria.Type, "category_id": criteria.CategoryID,
		"duration": criteria.Duration}
	if err := h.repo.RecordQuery(r.Context(), QueryRecord{
		UserID: principal.UserID, Query: criteria.Query, Normalized: criteria.Normalized,
		ResultCount: len(items), Duration: took, Filters: filters,
	}); err != nil {
		// 统计失败不影响检索结果。
		h.log.Warn("search: record query failed", "error", err)
	}

	minMS, maxMS := durationBounds(criteria.Duration)
	w.Header().Set("Cache-Control", "private, no-store")
	httpserver.JSON(w, http.StatusOK, Response{
		Query: criteria.Query, Items: items, NextCursor: next, Count: len(items),
		TookMS: took.Milliseconds(),
		Filters: AppliedFilters{
			Type: criteria.Type, Category: r.URL.Query().Get("category"),
			Duration: criteria.Duration, DurationMS: [2]int64{minMS, maxMS},
		},
	})
}

func (h *Handler) hot(w http.ResponseWriter, r *http.Request) {
	queries, err := h.repo.HotQueries(r.Context(), 10)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": queries, "window": "7d"})
}

func (h *Handler) categories(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.Categories(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=600")
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) category(w http.ResponseWriter, r *http.Request) {
	profile, err := h.repo.Category(r.Context(), r.PathValue("slug"))
	if errors.Is(err, ErrNotFound) {
		httpserver.Error(w, http.StatusNotFound, "not_found", "category not found",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	cursor, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("cursor")), 10, 64)
	if cursor < 0 {
		cursor = 0
	}
	entries, err := h.repo.ListByCategory(r.Context(), profile.ID, 0, cursor, 24)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	items := make([]feed.Item, 0, len(entries))
	for _, entry := range entries {
		item, composeErr := h.feed.Compose(r.Context(), entry, false)
		if composeErr != nil {
			h.log.Warn("category: skip item", "content_id", entry.ID, "error", composeErr)
			continue
		}
		items = append(items, item)
	}
	h.attachTags(r.Context(), entries, items)

	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"profile": profile, "items": items, "count": len(items),
	})
}

// attachTags decorates items with their tags in one extra query per page.
func (h *Handler) attachTags(ctx context.Context, entries []feed.Entry, items []feed.Item) {
	if len(entries) == 0 {
		return
	}
	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	tags, err := h.repo.TagsFor(ctx, ids)
	if err != nil {
		h.log.Warn("search: load tags failed", "error", err)
		return
	}
	for index := range items {
		if names := tags[items[index].ID]; len(names) > 0 {
			// Tags reuse Item.Description? No: keep the payload explicit via Extra.
			items[index].Tags = names
		}
	}
}

// parseCriteria validates and normalises every query parameter.
func parseCriteria(r *http.Request, viewerID int64) (Criteria, error) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	// 空白折叠：多个空格/换行视为单个空格，避免用空白构造高成本查询。
	query = strings.Join(strings.Fields(query), " ")
	if utf8.RuneCountInString(query) < MinQueryRunes {
		return Criteria{}, errQueryTooShort
	}
	if utf8.RuneCountInString(query) > MaxQueryRunes {
		return Criteria{}, errQueryTooLong
	}
	for _, symbol := range query {
		if unicode.IsControl(symbol) {
			return Criteria{}, errQueryChars
		}
	}

	criteria := Criteria{
		Query: query, Normalized: strings.ToLower(query), ViewerID: viewerID,
	}
	switch kind := strings.TrimSpace(r.URL.Query().Get("type")); kind {
	case "":
	case "video", "image":
		criteria.Type = kind
	default:
		return Criteria{}, errBadFilter
	}

	duration, _ := strconv.Atoi(r.URL.Query().Get("duration"))
	if duration < 0 || duration > 3 {
		return Criteria{}, errBadFilter
	}
	criteria.Duration = duration
	return criteria, nil
}

// likePattern escapes LIKE metacharacters. Escaping is for correctness: the
// term is bound as a parameter, so this is not the injection defence (§9.2).
func likePattern(term string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
	return "%" + escaped + "%"
}

// acquire enforces the fixed 5-second per-account search interval.
func (h *Handler) acquire(ctx context.Context, userID int64) (bool, time.Duration, error) {
	if h.redis == nil {
		return true, 0, nil
	}
	key := "search:cooldown:" + strconv.FormatInt(userID, 10)
	ok, err := h.redis.SetNX(ctx, key, "1", SearchCooldown).Result()
	if err != nil {
		return false, 0, err
	}
	if ok {
		return true, 0, nil
	}
	ttl, err := h.redis.TTL(ctx, key).Result()
	if err != nil || ttl < 0 {
		ttl = SearchCooldown
	}
	return false, ttl, nil
}

func parseCursor(raw string) (float64, int64) {
	raw = strings.TrimSpace(raw)
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

func formatCursor(score float64, id int64) string {
	return strconv.FormatFloat(score, 'g', -1, 64) + ":" + strconv.FormatInt(id, 10)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		httpserver.Error(w, http.StatusUnauthorized, "unauthorized", "login required", requestID)
	case errors.Is(err, errQueryTooShort):
		httpserver.Error(w, http.StatusBadRequest, "query_too_short", err.Error(), requestID)
	case errors.Is(err, errQueryTooLong):
		httpserver.Error(w, http.StatusBadRequest, "query_too_long", err.Error(), requestID)
	case errors.Is(err, errQueryChars):
		httpserver.Error(w, http.StatusBadRequest, "query_invalid", err.Error(), requestID)
	case errors.Is(err, errBadFilter):
		httpserver.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), requestID)
	case errors.Is(err, errCategory):
		httpserver.Error(w, http.StatusBadRequest, "invalid_category", err.Error(), requestID)
	default:
		h.fail(w, r, err)
	}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("search error", "error", err, "path", r.URL.Path)
	httpserver.Error(w, http.StatusInternalServerError, "internal_error",
		"unexpected server error", httpserver.RequestIDFromContext(r.Context()))
}
