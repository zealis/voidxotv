package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
)

type ctxKey string

const requestIDKey ctxKey = "request-id"

// RequestIDFromContext returns the id attached by the RequestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok {
		return value
	}
	return ""
}

type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in the order they are listed.
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 128 {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(buf[:])
}

// Recover keeps a panic from taking down the process and never returns a stack trace.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if log != nil {
						log.Error("panic recovered", "request_id", RequestIDFromContext(r.Context()),
							"path", r.URL.Path, "panic", recovered, "stack", string(debug.Stack()))
					}
					Error(w, http.StatusInternalServerError, "internal_error", "unexpected server error", RequestIDFromContext(r.Context()))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(body []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(body)
}

// AccessLog emits one structured line per request without secrets or bodies.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)
			if log == nil {
				return
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			log.Info("http", "request_id", RequestIDFromContext(r.Context()),
				"method", r.Method, "path", r.URL.Path, "status", status,
				"duration_ms", time.Since(start).Milliseconds())
		})
	}
}

// SecurityHeaders writes CSP at the server, never as a client <meta> tag (开发技术规范 §11.5).
func SecurityHeaders(cfg config.Config) Middleware {
	csp := strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"media-src 'self' blob: https:",
		// blob: 是 hls.js 取 MSE playlist/分片的通道（播放器把签名 master 包成
		// Blob URL 再 fetch）；缺它整个 HLS 播放在浏览器里直接失败。
		"connect-src 'self' blob: " + cfg.APIOrigin,
	}, "; ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			header.Set("Content-Security-Policy", csp)
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
			if cfg.IsProduction() {
				header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS uses an explicit allow-list; credentials are only sent for allowed origins.
func CORS(allowedOrigins []string) Middleware {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			header := w.Header()
			header.Add("Vary", "Origin")
			if _, ok := allowed[origin]; ok {
				header.Set("Access-Control-Allow-Origin", origin)
				header.Set("Access-Control-Allow-Credentials", "true")
				header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				header.Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Request-Id")
				header.Set("Access-Control-Expose-Headers", "X-Request-Id")
				header.Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
