// 最小指标采集（技术规范 §13.2：建议 OpenTelemetry 之外的轻量基线）：
// HTTP 请求计数 / 延迟直方图按「方法 + 路由模式」聚合（ServeMux 的 r.Pattern
// 保证标签基数有界，查询串不进标签），外加数据库连接池状态与 Outbox 队列深度，
// 以 Prometheus 文本格式在 /metrics 暴露。不含任何请求级敏感信息。
package httpserver

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// durationBuckets 是请求延迟直方图的桶边界（秒），覆盖健康检查到慢转码回调。
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type routeStat struct {
	counts  map[int]int64
	total   float64
	count   int64
	buckets [11]int64 // 与 durationBuckets 长度一致（数组长度需常量）
}

// Metrics 是进程内的指标注册表；并发安全，标签基数受路由模式约束。
type Metrics struct {
	mu     sync.Mutex
	routes map[string]*routeStat
}

func NewMetrics() *Metrics {
	return &Metrics{routes: make(map[string]*routeStat)}
}

// Middleware 记录每个请求的路由、状态码与延迟。
func (m *Metrics) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &metricsResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			seconds := time.Since(start).Seconds()
			route := r.Pattern
			// 兜底 "/" 会吞掉所有未匹配请求：统一打 catch_all 标签。
			if route == "" || route == "/" {
				route = "catch_all"
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			// r.Pattern 已带方法前缀（如 "GET /api/v1/feed"），直接作键。
			stat := m.routes[route]
			if stat == nil {
				stat = &routeStat{counts: make(map[int]int64)}
				m.routes[route] = stat
			}
			stat.counts[recorder.status]++
			stat.count++
			stat.total += seconds
			for i, bound := range durationBuckets {
				if seconds <= bound {
					stat.buckets[i]++
				}
			}
		})
	}
}

// metricsResponseWriter 捕获状态码（延迟在中间件层测量，与写出时机解耦）。
type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Prometheus 输出指标文本；db 非 nil 时附带连接池与 Outbox 队列深度。
func (m *Metrics) Prometheus(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		routes := make([]string, 0, len(m.routes))
		for key := range m.routes {
			routes = append(routes, key)
		}
		sort.Strings(routes)
		var builder strings.Builder
		for _, key := range routes {
			stat := m.routes[key]
			method, route := parseRouteKey(key)
			statuses := make([]int, 0, len(stat.counts))
			for status := range stat.counts {
				statuses = append(statuses, status)
			}
			sort.Ints(statuses)
			base := "yejie_http_requests_total" + labels(method, route, "")
			for _, status := range statuses {
				builder.WriteString(base)
				builder.WriteString(`{status="`)
				builder.WriteString(strconv.Itoa(status))
				builder.WriteString(`"} `)
				builder.WriteString(strconv.FormatInt(stat.counts[status], 10))
				builder.WriteByte('\n')
			}
			builder.WriteString("yejie_http_request_duration_seconds_count")
			builder.WriteString(labels(method, route, ""))
			builder.WriteByte(' ')
			builder.WriteString(strconv.FormatInt(stat.count, 10))
			builder.WriteByte('\n')
			builder.WriteString("yejie_http_request_duration_seconds_sum")
			builder.WriteString(labels(method, route, ""))
			builder.WriteByte(' ')
			builder.WriteString(strconv.FormatFloat(stat.total, 'f', -1, 64))
			builder.WriteByte('\n')
			for i, bound := range durationBuckets {
				builder.WriteString("yejie_http_request_duration_seconds_bucket")
				builder.WriteString(labels(method, route, strconv.FormatFloat(bound, 'f', -1, 64)))
				builder.WriteByte(' ')
				builder.WriteString(strconv.FormatInt(stat.buckets[i], 10))
				builder.WriteByte('\n')
			}
			// 直方图必须包含 +Inf 桶（与 count 相等）。
			builder.WriteString("yejie_http_request_duration_seconds_bucket")
			builder.WriteString(labels(method, route, "+Inf"))
			builder.WriteByte(' ')
			builder.WriteString(strconv.FormatInt(stat.count, 10))
			builder.WriteByte('\n')
		}
		m.mu.Unlock()

		if db != nil {
			stat := db.Stat()
			writeGauge(&builder, "yejie_db_pool_acquired", float64(stat.AcquiredConns()))
			writeGauge(&builder, "yejie_db_pool_idle", float64(stat.IdleConns()))
			writeGauge(&builder, "yejie_db_pool_total", float64(stat.TotalConns()))
			writeGauge(&builder, "yejie_db_pool_max", float64(stat.MaxConns()))
			pending, failed := outboxDepth(r.Context(), db)
			writeGauge(&builder, "yejie_outbox_pending", float64(pending))
			writeGauge(&builder, "yejie_outbox_failed", float64(failed))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(builder.String()))
	}
}

// outboxDepth 统计 Outbox 队列深度（pending / failed），供部署侧告警接线。
func outboxDepth(ctx context.Context, db *pgxpool.Pool) (pending, failed int64) {
	var p, f int64
	if err := db.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'pending'),
		count(*) FILTER (WHERE status = 'failed')
		FROM outbox_events`).Scan(&p, &f); err != nil {
		return 0, 0
	}
	return p, f
}

func writeGauge(builder *strings.Builder, name string, value float64) {
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	builder.WriteByte('\n')
}

func labels(method, route, le string) string {
	var builder strings.Builder
	builder.WriteString(`{method="`)
	builder.WriteString(method)
	builder.WriteString(`",route="`)
	builder.WriteString(route)
	builder.WriteString(`"`)
	if le != "" {
		builder.WriteString(`,le="`)
		builder.WriteString(le)
		builder.WriteString(`"`)
	}
	builder.WriteString(`}`)
	return builder.String()
}

func parseRouteKey(key string) (method, route string) {
	// 键格式是 r.Pattern：「METHOD 路径」（方法在前）。无空格的键（catch_all）
	// method 留空。
	parts := strings.SplitN(key, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", key
}
