package gifs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 每页条数由配置决定（接口上限 20），这里只留请求页码的边界。
const (
	maxPage      = 50
	rateLimitMax = 20 // 每人每分钟
)

// httpClient 单独拿出来，测试里可以换成 httptest 的 client。
var httpClient = &http.Client{Timeout: 8 * time.Second}

type Handler struct {
	db    *pgxpool.Pool
	auth  *auth.Handler
	redis *redis.Client
	log   *slog.Logger
}

func NewHandler(db *pgxpool.Pool, authHandler *auth.Handler, client *redis.Client,
	log *slog.Logger) *Handler {
	return &Handler{db: db, auth: authHandler, redis: client, log: log}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// 搜索要拿本站密钥去敲上游，等于替所有访客买单，所以必须登录并按人限流。
	mux.Handle("GET /api/v1/gifs/search", h.auth.RequireAuth(http.HandlerFunc(h.search)))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	httpserver.Error(w, status, code, message, httpserver.RequestIDFromContext(r.Context()))
}

// searchResult 是缓存与响应共用的结构（图片地址 + 翻页信息）。
type searchResult struct {
	Items   []string `json:"items"`
	Page    int      `json:"page"`
	MaxPage int      `json:"max_page"`
	Count   int      `json:"count"`
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		h.fail(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	cfg, err := LoadConfig(r.Context(), h.db)
	if err != nil {
		h.log.Error("gifs: load config", "error", err)
		h.fail(w, r, http.StatusInternalServerError, "internal_error", "表情包服务不可用")
		return
	}
	if !cfg.Ready() {
		// 明确区别于「没有结果」：管理员没配密钥时前端要提示去后台填。
		h.fail(w, r, http.StatusBadRequest, "gif_not_configured", "表情包源尚未配置")
		return
	}
	query := []rune(strings.TrimSpace(r.URL.Query().Get("q")))
	// 上游按汉字限 10 个字，超了它也截断，这里先截保持一致。
	if len(query) > 10 {
		query = query[:10]
	}
	keyword := string(query)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	if !h.allow(r.Context(), principal.UserID) {
		h.fail(w, r, http.StatusTooManyRequests, "rate_limited", "搜索太频繁，请一分钟后再试")
		return
	}

	// 缓存键带上线路 / 关键词 / 页码 / 条数：管理员换了线路或改了条数，旧条目自然不再命中。
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%d",
		cfg.Provider, cfg.ApihzLine, keyword, page, cfg.ApihzLimit)))
	cacheKey := hex.EncodeToString(sum[:])

	if cfg.CacheDays > 0 {
		var raw []byte
		err := h.db.QueryRow(r.Context(), `
			SELECT payload FROM gif_search_cache
			WHERE k = $1 AND expire_at > now()`, cacheKey).Scan(&raw)
		if err == nil {
			var cached searchResult
			if json.Unmarshal(raw, &cached) == nil {
				httpserver.JSON(w, http.StatusOK, map[string]any{
					"items": cached.Items, "page": cached.Page, "max_page": cached.MaxPage,
					"count": cached.Count, "cached": true,
				})
				return
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			// 缓存读失败不该让搜索整体失败，继续走上游。
			h.log.Warn("gifs: read cache", "error", err)
		}
	}

	result, err := h.fetch(r.Context(), cfg, keyword, page)
	if err != nil {
		h.log.Warn("gifs: upstream", "error", err, "query", keyword, "page", page)
		h.fail(w, r, http.StatusBadGateway, "gif_upstream_failed", "表情包服务暂时不可用，请稍后再试")
		return
	}
	if cfg.CacheDays > 0 {
		h.writeCache(r.Context(), cacheKey, result, cfg.CacheDays)
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": result.Items, "page": result.Page, "max_page": result.MaxPage,
		"count": result.Count, "cached": false,
	})
}

// allow 是每人每分钟的窗口限流：拿本站密钥刷第三方接口等于烧配额。
// Redis 不可用时放行（宁可能用，不要把功能整体打死）。
func (h *Handler) allow(ctx context.Context, userID int64) bool {
	if h.redis == nil {
		return true
	}
	key := fmt.Sprintf("gif:rl:%d:%d", userID, time.Now().Unix()/60)
	count, err := h.redis.Incr(ctx, key).Result()
	if err != nil {
		h.log.Warn("gifs: rate limit", "error", err)
		return true
	}
	if count == 1 {
		h.redis.Expire(ctx, key, 70*time.Second)
	}
	return count <= rateLimitMax
}

// fetch 调 API 盒子「表情包」接口（type=1 热门、type=2 按关键词搜索）。
func (h *Handler) fetch(ctx context.Context, cfg Config, keyword string, page int) (searchResult, error) {
	params := url.Values{}
	params.Set("id", cfg.ApihzID)
	params.Set("key", cfg.ApihzKey)
	params.Set("limit", strconv.Itoa(cfg.ApihzLimit))
	params.Set("page", strconv.Itoa(page))
	if keyword == "" {
		params.Set("type", "1")
	} else {
		params.Set("type", "2")
		params.Set("words", keyword)
	}
	// 不传 uip（访客 IP）：接口盒子把它写成可选，但非会员传了会被直接拒绝，
	// 而它对我们的限流没有任何作用（限流是自己按人算的）。
	endpoint := cfg.UpstreamHost() + "/api/img/apihzbqb.php?" + params.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return searchResult{}, err
	}
	request.Header.Set("User-Agent", "yejie-gifs/1.0")
	response, err := httpClient.Do(request)
	if err != nil {
		return searchResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return searchResult{}, err
	}

	var payload struct {
		Code    int      `json:"code"`
		Msg     string   `json:"msg"`
		Res     []string `json:"res"`
		Page    int      `json:"page"`
		MaxPage int      `json:"maxpage"`
		Count   int      `json:"count"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return searchResult{}, fmt.Errorf("gifs: decode upstream: %w", err)
	}
	if payload.Code != 200 {
		return searchResult{}, fmt.Errorf("gifs: upstream code %d: %s", payload.Code, payload.Msg)
	}

	items := make([]string, 0, len(payload.Res))
	for _, raw := range payload.Res {
		if cleaned := normalizeImageURL(raw); cleaned != "" {
			items = append(items, cleaned)
		}
	}
	result := searchResult{Items: items, Page: page, MaxPage: payload.MaxPage, Count: len(items)}
	if result.Page < 1 {
		result.Page = page
	}
	if result.MaxPage < 1 {
		result.MaxPage = 1
	}
	return result, nil
}

// normalizeImageURL 只留 https 直链，并把 http 升级成 https：
// 页面是 https，浏览器会拦 http 图片；上游偶尔给 http 的 CDN 地址。
func normalizeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "http://")
	if !strings.HasPrefix(raw, "https://") {
		return ""
	}
	if len(raw) > 300 {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

// writeCache 只在真调成功后写：上游报错不落缓存，免得把失败当结论记住。
func (h *Handler) writeCache(ctx context.Context, key string, result searchResult, days int) {
	payload, err := json.Marshal(result)
	if err != nil {
		return
	}
	if _, err := h.db.Exec(ctx, `
		INSERT INTO gif_search_cache (k, payload, expire_at)
		VALUES ($1, $2, now() + ($3 || ' days')::interval)
		ON CONFLICT (k) DO UPDATE SET payload = EXCLUDED.payload, expire_at = EXCLUDED.expire_at`,
		key, payload, strconv.Itoa(days)); err != nil {
		h.log.Warn("gifs: write cache", "error", err)
		return
	}
	// 搜索词是无限的，光靠过期清不干净：写入时顺手清过期行，再把总量压到 500 行以内
	// （按 expire_at 倒序保留，等价于留最新的那批）。
	_, _ = h.db.Exec(ctx, `DELETE FROM gif_search_cache WHERE expire_at < now()`)
	_, _ = h.db.Exec(ctx, `
		DELETE FROM gif_search_cache WHERE k IN (
			SELECT k FROM gif_search_cache ORDER BY expire_at DESC OFFSET 500
		)`)
}
