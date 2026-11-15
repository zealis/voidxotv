// Package app assembles the HTTP router and its dependencies.
package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/content"
	"github.com/yejie/voidxotv/internal/modules/danmaku"
	"github.com/yejie/voidxotv/internal/modules/delivery"
	"github.com/yejie/voidxotv/internal/modules/feed"
	"github.com/yejie/voidxotv/internal/modules/gifs"
	"github.com/yejie/voidxotv/internal/modules/interactions"
	"github.com/yejie/voidxotv/internal/modules/notifications"
	"github.com/yejie/voidxotv/internal/modules/playback"
	"github.com/yejie/voidxotv/internal/modules/search"
	"github.com/yejie/voidxotv/internal/modules/seo"
	"github.com/yejie/voidxotv/internal/modules/settings"
	"github.com/yejie/voidxotv/internal/modules/upload"
	"github.com/yejie/voidxotv/internal/platform/buildinfo"
	"github.com/yejie/voidxotv/internal/platform/cache"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// Dependencies are injected by cmd/api; modules never construct their own connections.
type Dependencies struct {
	Cfg           config.Config
	Log           *slog.Logger
	DB            *pgxpool.Pool
	Metrics       *httpserver.Metrics
	Cache         *cache.Store
	Auth          *auth.Handler
	Content       *content.Handler
	Upload        *upload.Handler
	Playback      *playback.Handler
	Feed          *feed.Handler
	Delivery      *delivery.Handler
	SEO           *seo.Handler
	Admin         *admin.Handler
	Interactions  *interactions.Handler
	Gifs          *gifs.Handler
	Danmaku       *danmaku.Handler
	Search        *search.Handler
	Notifications *notifications.Handler
	Settings      *settings.Handler
}

// NewRouter registers all routes and applies the shared middleware chain.
func NewRouter(deps Dependencies) http.Handler {
	mux := http.NewServeMux()
	// 健康与指标只给本机（容器探针 / 运维）：反代就算误转发，也不会把
	// 版本号、依赖状态、连接池指标暴露给外部。
	// 存活探针同时回报版本号：排障时能一眼确认线上跑的是哪一版。
	mux.Handle("GET /health/live", internalOnly(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			httpserver.JSON(w, http.StatusOK, map[string]string{
				"status":  "ok",
				"version": buildinfo.Version,
			})
		})))
	mux.Handle("GET /health/ready", internalOnly(http.HandlerFunc(readyHandler(deps))))
	// 指标端点（§13.2 最小基线）：路由计数 / 延迟直方图 / 连接池 / Outbox 深度。
	if deps.Metrics != nil {
		mux.Handle("GET /metrics", internalOnly(deps.Metrics.Prometheus(deps.DB)))
	}
	if deps.Auth != nil {
		deps.Auth.RegisterRoutes(mux)
	}
	if deps.Content != nil {
		deps.Content.RegisterRoutes(mux)
	}
	if deps.Upload != nil {
		deps.Upload.RegisterRoutes(mux)
	}
	if deps.Playback != nil {
		deps.Playback.RegisterRoutes(mux)
	}
	if deps.Feed != nil {
		deps.Feed.RegisterRoutes(mux)
	}
	if deps.Delivery != nil {
		deps.Delivery.RegisterRoutes(mux)
	}
	if deps.SEO != nil {
		deps.SEO.RegisterRoutes(mux)
	}
	if deps.Admin != nil {
		deps.Admin.RegisterRoutes(mux)
	}
	if deps.Interactions != nil {
		deps.Interactions.RegisterRoutes(mux)
	}
	if deps.Gifs != nil {
		deps.Gifs.RegisterRoutes(mux)
	}
	if deps.Danmaku != nil {
		deps.Danmaku.RegisterRoutes(mux)
	}
	if deps.Search != nil {
		deps.Search.RegisterRoutes(mux)
	}
	if deps.Notifications != nil {
		deps.Notifications.RegisterRoutes(mux)
	}
	if deps.Settings != nil {
		deps.Settings.RegisterRoutes(mux)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpserver.Error(w, http.StatusNotFound, "not_found", "resource not found",
			httpserver.RequestIDFromContext(r.Context()))
	})

	chain := []httpserver.Middleware{
		httpserver.RequestID,
		httpserver.Recover(deps.Log),
	}
	if deps.Metrics != nil {
		chain = append(chain, deps.Metrics.Middleware())
	}
	chain = append(chain,
		httpserver.AccessLog(deps.Log),
		httpserver.SecurityHeaders(deps.Cfg),
		httpserver.CORS(deps.Cfg.AllowedOrigins),
	)
	return httpserver.Chain(mux, chain...)
}

// readyHandler checks both dependencies within 2 seconds; a failed check is 503, never a bypass.
func readyHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		checks := map[string]string{}
		failed := false
		if deps.DB == nil {
			checks["postgres"] = "unconfigured"
			failed = true
		} else if err := deps.DB.Ping(ctx); err != nil {
			checks["postgres"] = "unavailable"
			failed = true
		} else {
			checks["postgres"] = "ok"
		}
		if deps.Cache == nil {
			checks["redis"] = "unconfigured"
			failed = true
		} else if err := deps.Cache.Ping(ctx); err != nil {
			checks["redis"] = "unavailable"
			failed = true
		} else {
			checks["redis"] = "ok"
		}

		if failed {
			httpserver.Error(w, http.StatusServiceUnavailable, "not_ready", "dependencies unavailable",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		// 不返回各依赖的具体状态：健康信息只对运维有意义，不该暴露给外部。
		httpserver.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// internalOnly 只允许本机直连访问。容器健康检查走 127.0.0.1 且不带任何代理头；
// 反向代理转发会带 X-Forwarded-For / X-Real-IP / Forwarded，一律按外部请求处理。
func internalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isInternalRequest(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isInternalRequest(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" ||
		r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.Trim(r.RemoteAddr, "[]")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
