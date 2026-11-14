// Package gifs owns「评论 GIF 表情包」：后台可填的图源配置、服务端代理搜索与结果缓存。
//
// 为什么服务端代理而不是浏览器直连：图源用长期密钥（API 盒子），只该存在服务端；
// 前端拿到的是本站的搜索结果，密钥不出站。图源按天限额，所以结果按「线路+关键词+页」
// 缓存到 gif_search_cache，缓存期内不重复请求上游。
package gifs

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 图源开关：off 表示没配置，前端应提示「未配置」而不是给个空列表。
const (
	ProviderOff   = "off"
	ProviderApihz = "apihz"
)

// 评论正文里的 GIF 标记：[[gif:https://...]]。存文本而不是另开表，
// 是为了让评论列表 / 举报 / 审计这些既有链路零改动继续工作。
const (
	MarkerPrefix = "[[gif:"
	MarkerSuffix = "]]"
)

// 允许出现在评论里的图源主机：只有我们集成过的图源官方域名。
// 不放开任意域名，否则评论能嵌任意 https 图片，等于给别人一个追踪像素。
var allowedHosts = []string{"apihz.cn", "klipy.com"}

// Config 是后台「功能开关 → GIF 表情包」里的那一组配置。
type Config struct {
	Provider   string `json:"provider"`
	ApihzLine  string `json:"apihz_line"`
	ApihzID    string `json:"apihz_id"`
	ApihzKey   string `json:"apihz_key"`
	ApihzLimit int    `json:"apihz_limit"`
	CacheDays  int    `json:"cache_days"`
}

func defaults() Config {
	return Config{Provider: ProviderOff, ApihzLine: "cn", ApihzLimit: 20, CacheDays: 7}
}

// Normalize 把后台传进来的值收敛到合法范围：不信任表单，
// 线路只认 cn / vip，条数 1–20（接口上限），缓存 0–90 天。
func (c Config) Normalize() Config {
	out := c
	switch strings.TrimSpace(out.Provider) {
	case ProviderApihz:
		out.Provider = ProviderApihz
	default:
		out.Provider = ProviderOff
	}
	if strings.TrimSpace(out.ApihzLine) == "vip" {
		out.ApihzLine = "vip"
	} else {
		out.ApihzLine = "cn"
	}
	out.ApihzID = strings.TrimSpace(out.ApihzID)
	out.ApihzKey = strings.TrimSpace(out.ApihzKey)
	if out.ApihzLimit < 1 {
		out.ApihzLimit = 1
	}
	if out.ApihzLimit > 20 {
		out.ApihzLimit = 20
	}
	if out.CacheDays < 0 {
		out.CacheDays = 0
	}
	if out.CacheDays > 90 {
		out.CacheDays = 90
	}
	return out
}

// Ready 判断当前图源是否具备调用条件（选了 apihz 且 id/key 都填了）。
func (c Config) Ready() bool {
	return c.Provider == ProviderApihz && c.ApihzID != "" && c.ApihzKey != ""
}

// UpstreamHost 是 apihz 的两条线路主机，白名单取值，不接受外部传入地址（防 SSRF）。
func (c Config) UpstreamHost() string {
	if c.ApihzLine == "vip" {
		return "https://vip.apihz.cn"
	}
	return "https://cn.apihz.cn"
}

// LoadConfig 读 system_settings 里的 6 个键；缺键回落默认值。
func LoadConfig(ctx context.Context, db *pgxpool.Pool) (Config, error) {
	cfg := defaults()
	rows, err := db.Query(ctx, `
		SELECT key, value FROM system_settings WHERE key LIKE 'gif.%'`)
	if err != nil {
		return cfg, fmt.Errorf("gifs: load config: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return cfg, fmt.Errorf("gifs: scan config: %w", err)
		}
		switch key {
		case "gif.provider":
			cfg.Provider = value
		case "gif.apihz_line":
			cfg.ApihzLine = value
		case "gif.apihz_id":
			cfg.ApihzID = value
		case "gif.apihz_key":
			cfg.ApihzKey = value
		case "gif.apihz_limit":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.ApihzLimit = parsed
			}
		case "gif.cache_days":
			if parsed, err := strconv.Atoi(value); err == nil {
				cfg.CacheDays = parsed
			}
		}
	}
	return cfg.Normalize(), rows.Err()
}

// SaveConfig 幂等 upsert；updated_by 只用于追溯谁改的。
func SaveConfig(ctx context.Context, db *pgxpool.Pool, adminID int64, cfg Config) (Config, error) {
	normalized := cfg.Normalize()
	pairs := [][2]string{
		{"gif.provider", normalized.Provider},
		{"gif.apihz_line", normalized.ApihzLine},
		{"gif.apihz_id", normalized.ApihzID},
		{"gif.apihz_key", normalized.ApihzKey},
		{"gif.apihz_limit", strconv.Itoa(normalized.ApihzLimit)},
		{"gif.cache_days", strconv.Itoa(normalized.CacheDays)},
	}
	for _, pair := range pairs {
		if _, err := db.Exec(ctx, `
			INSERT INTO system_settings (key, value, updated_by, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (key) DO UPDATE SET
			  value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			pair[0], pair[1], adminID); err != nil {
			return normalized, fmt.Errorf("gifs: save %s: %w", pair[0], err)
		}
	}
	return normalized, nil
}

// AllowedHost 只放行集成过的图源官方域名（含子域）。
func AllowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, allowed := range allowedHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// ValidGifURL 判断一个地址能不能进评论：https、无凭据、无端口、主机在白名单、长度可控。
// https 不是洁癖：本站是 https 页面，浏览器会直接拦掉 http 图片。
func ValidGifURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 300 {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		return false
	}
	return AllowedHost(parsed.Hostname())
}

// Marker 包一条合法的 GIF 地址成评论正文标记。
func Marker(rawURL string) string {
	return MarkerPrefix + strings.TrimSpace(rawURL) + MarkerSuffix
}

// Markers 抽出正文里所有 GIF 标记的地址（顺序保留）。
func Markers(body string) []string {
	var found []string
	rest := body
	for {
		start := strings.Index(rest, MarkerPrefix)
		if start < 0 {
			return found
		}
		rest = rest[start+len(MarkerPrefix):]
		end := strings.Index(rest, MarkerSuffix)
		if end < 0 {
			return found
		}
		found = append(found, strings.TrimSpace(rest[:end]))
		rest = rest[end+len(MarkerSuffix):]
	}
}

// StripMarkers 把标记换成可读文字，供通知摘要 / 后台评论管理这类纯文本场景使用。
func StripMarkers(body string) string {
	out := body
	for _, item := range Markers(body) {
		out = strings.ReplaceAll(out, Marker(item), "[GIF]")
	}
	return strings.TrimSpace(out)
}
