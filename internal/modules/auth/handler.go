package auth

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
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
}

// SetVerificationStore 注入验证码存储；未注入时找回密码接口返回 503。
func (h *Handler) SetVerificationStore(store *VerificationStore) { h.verification = store }

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
	// 找回密码（§11.7）：公开端点，频控在处理器内做。
	mux.HandleFunc("GET /api/v1/auth/captcha", h.captcha)
	mux.HandleFunc("POST /api/v1/auth/password/reset-request", h.resetRequestCode)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetConfirmHandler)
}

type registerRequest struct {
	Handle   string `json:"handle"`
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
		Handle: request.Handle, Email: request.Email, Password: request.Password,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, map[string]any{
		"id": user.ID, "handle": user.Handle, "role": user.Role,
	})
}

type loginRequest struct {
	Handle   string `json:"handle"`
	Password string `json:"password"`
	DeviceID string `json:"device_id"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	token, user, err := h.svc.Login(r.Context(), LoginInput{
		Handle:    request.Handle,
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
		SameSite: http.SameSiteLaxMode,
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
		Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
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
	case errors.Is(err, ErrInvalidHandle), errors.Is(err, ErrWeakPassword),
		errors.Is(err, ErrInvalidEmail), errors.Is(err, httpserver.ErrInvalidJSON):
		status, code, message = http.StatusBadRequest, "invalid_input", err.Error()
	case errors.Is(err, ErrUserExists):
		status, code, message = http.StatusConflict, "user_exists", "handle or email already registered"
	case errors.Is(err, ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "invalid_credentials", "invalid handle or password"
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

// clientIP uses the peer address only; X-Forwarded-For is ignored unless a trusted proxy is configured (§11.6).
func clientIP(r *http.Request) string {
	return strings.Split(r.RemoteAddr, ":")[0]
}
