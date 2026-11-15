package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/yejie/voidxotv/internal/platform/avatar"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/httpx"
)

const (
	sessionCookieDev  = "yj_session"
	sessionCookieProd = "__Host-session"
	csrfHeader        = "X-CSRF-Token"
)

// Handler exposes the auth endpoints and the two request middlewares.
type Handler struct {
	svc *Service
	cfg config.Config
	log *slog.Logger
	// verification 管找回密码的验证码 / 频控 / 图形验证码；用 setter 注入。
	verification *VerificationStore
	// defaultAvatar 给新用户异步分配一张随机头像；S3 不可用时为 nil，注册照常可用。
	defaultAvatar *DefaultAvatar
}

// SetVerificationStore 注入验证码存储；未注入时找回密码接口返回 503。
func (h *Handler) SetVerificationStore(store *VerificationStore) { h.verification = store }

// SetDefaultAvatar 注入默认头像分配器（需要对象存储，由组装层注入）。
func (h *Handler) SetDefaultAvatar(avatar *DefaultAvatar) { h.defaultAvatar = avatar }

func NewHandler(svc *Service, cfg config.Config, log *slog.Logger) *Handler {
	return &Handler{svc: svc, cfg: cfg, log: log}
}

func (h *Handler) cookieName() string {
	if h.cfg.CookieSecure {
		return sessionCookieProd
	}
	return sessionCookieDev
}

// RegisterRoutes wires all auth endpoints under /api/v1.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.Handle("POST /api/v1/auth/logout", h.RequireAuth(h.RequireCSRF(http.HandlerFunc(h.logout))))
	mux.Handle("GET /api/v1/auth/me", h.RequireAuth(http.HandlerFunc(h.me)))
	mux.Handle("GET /api/v1/auth/csrf", h.RequireAuth(http.HandlerFunc(h.csrf)))
	// 敏感操作二次确认：重新输入密码换 5 分钟令牌，删除类接口要求带上。
	mux.Handle("POST /api/v1/auth/step-up", h.RequireAuth(h.RequireCSRF(http.HandlerFunc(h.stepUp))))
	// 找回密码（§11.7）：公开端点，频控在处理器内做。
	mux.HandleFunc("GET /api/v1/auth/captcha", h.captcha)
	mux.HandleFunc("POST /api/v1/auth/password/reset-request", h.resetRequestCode)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetConfirmHandler)
	// 随机头像预览代理：前端 CSP 只允许 'self' 与自有媒体源，不能直接 <img> 外链
	// dicebear.com；这里按 seed 同源回一张 PNG，选中后才由写入接口落库。
	mux.HandleFunc("GET /api/v1/avatar/preview", h.avatarPreview)
}

// avatarPreview 是只读的图片代理，只接受本系统生成的 12 字节 hex seed。
func (h *Handler) avatarPreview(w http.ResponseWriter, r *http.Request) {
	seed := r.URL.Query().Get("seed")
	if !avatar.ValidSeed(seed) {
		http.Error(w, "invalid seed", http.StatusBadRequest)
		return
	}
	data, err := avatar.FetchPNG(r.Context(), seed)
	if err != nil {
		h.log.Warn("avatar preview failed", "error", err)
		http.Error(w, "avatar unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", avatar.CacheHeader)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// registerRequest 只收邮箱与密码：夜街号与默认昵称由服务端生成。
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var request registerRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	user, err := h.svc.Register(r.Context(), RegisterInput{
		Email: request.Email, Password: request.Password,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 异步分配默认头像：不阻塞注册响应，也不把外部图片服务的可用性绑到注册链路上。
	// 用独立的 context + 超时，避免请求结束后 r.Context() 被取消导致任务半途而废。
	if h.defaultAvatar != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), AssignTTL)
			defer cancel()
			h.defaultAvatar.Assign(ctx, user.ID, user.Handle)
		}()
	}
	httpserver.JSON(w, http.StatusCreated, map[string]any{
		"id": user.ID, "handle": user.Handle, "role": user.Role,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Account  string `json:"account"`
	Handle   string `json:"handle"` // 旧字段：前台已不再发送，保留给 curl / 既有脚本
	Password string `json:"password"`
	DeviceID string `json:"device_id"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	account := request.Email
	if account == "" {
		account = request.Account
	}
	if account == "" {
		account = request.Handle
	}
	token, user, err := h.svc.Login(r.Context(), LoginInput{
		Account:   account,
		Password:  request.Password,
		DeviceID:  request.DeviceID,
		IPAddress: clientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().UTC().Add(h.cfg.SessionAbsoluteTTL),
		MaxAge:   int(h.cfg.SessionAbsoluteTTL.Seconds()),
	})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": user.ID, "handle": user.Handle, "role": user.Role,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if err := h.svc.Logout(r.Context(), principal); err != nil {
		h.writeError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: h.cookieName(), Value: "", Path: "/", HttpOnly: true,
		Secure: h.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	httpserver.JSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"user_id": principal.UserID, "handle": principal.Handle, "roles": principal.Roles,
	})
}

func (h *Handler) csrf(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	httpserver.JSON(w, http.StatusOK, map[string]string{"csrf_token": principal.CSRF})
}

// RequireAuth reads the session cookie, validates it in Redis and injects the principal.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(h.cookieName())
		if err != nil || cookie.Value == "" {
			h.writeError(w, r, ErrUnauthorized)
			return
		}
		principal, err := h.svc.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

// OptionalPrincipal is used by public endpoints that personalize only for logged-in users.
func (h *Handler) OptionalPrincipal(r *http.Request) (Principal, bool) {
	cookie, err := r.Cookie(h.cookieName())
	if err != nil || cookie.Value == "" {
		return Principal{}, false
	}
	principal, err := h.svc.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		return Principal{}, false
	}
	return principal, true
}

// RequireCSRF compares the header with the token stored in the active session.
func (h *Handler) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.CSRF == "" {
			h.writeError(w, r, ErrUnauthorized)
			return
		}
		provided := r.Header.Get(csrfHeader)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(principal.CSRF)) != 1 {
			httpserver.Error(w, http.StatusForbidden, "csrf_failed", "invalid csrf token",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code, message := "internal_error", "unexpected server error"
	switch {
	case errors.Is(err, ErrWeakPassword),
		errors.Is(err, ErrInvalidEmail), errors.Is(err, httpserver.ErrInvalidJSON):
		status, code, message = http.StatusBadRequest, "invalid_input", err.Error()
	case errors.Is(err, ErrUserExists):
		status, code, message = http.StatusConflict, "user_exists", "email already registered"
	case errors.Is(err, ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect"
	case errors.Is(err, ErrAccountDisabled):
		status, code, message = http.StatusForbidden, "account_disabled", "account is not active"
	case errors.Is(err, ErrRateLimited):
		status, code, message = http.StatusTooManyRequests, "rate_limited", "too many login attempts"
		w.Header().Set("Retry-After", "60")
	case errors.Is(err, ErrUnauthorized):
		status, code, message = http.StatusUnauthorized, "unauthorized", "authentication required"
	}
	if status == http.StatusInternalServerError {
		h.log.Error("auth error", "error", err, "path", r.URL.Path)
	}
	httpserver.Error(w, status, code, message, httpserver.RequestIDFromContext(r.Context()))
}

// clientIP 交给统一的 httpx.ClientIP：默认只认 TCP 对端，只有直连来自受信
// 代理网段时才采信 X-Forwarded-For 的最右一跳（§11.6）。
func clientIP(r *http.Request) string {
	return httpx.ClientIP(r)
}
