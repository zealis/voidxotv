// Package admin implements the operations console: separate accounts, RBAC,
// moderation, reporting, statistics and hybrid-source configuration.
// 开发技术规范 §4.7 / §11.5 / §11.6：后台账号与普通用户体系分离，权限只在服务端判定。
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

const (
	cookieDev  = "yj_admin_session"
	cookieProd = "__Host-admin-session"
	csrfHeader = "X-CSRF-Token"
)

var (
	ErrUnauthorized = errors.New("admin: authentication required")
	ErrForbidden    = errors.New("admin: permission denied")
	ErrBadLogin     = errors.New("admin: invalid credentials")
)

// Permission keys are the only units the RBAC matrix understands.
const (
	PermStatsRead      = "stats:read"
	PermStatsAggregate = "stats:aggregate"
	PermReviewRead     = "reviews:read"
	PermReviewDecide   = "reviews:decide"
	PermReportRead     = "reports:read"
	PermReportResolve  = "reports:resolve"
	PermUserRead       = "users:read"
	PermUserSanction   = "users:sanction"
	PermTopicRead      = "topics:read"
	PermTopicWrite     = "topics:write"
	PermConnectorRead  = "connectors:read"
	PermConnectorWrite = "connectors:write"
	PermSettingsRead   = "settings:read"
	PermSettingsWrite  = "settings:write"
	PermAuditRead      = "audit:read"
	PermDeliveryAdmin  = "delivery:admin"
	// 账号管理只给超级管理员：能改角色/停用/重置口令，等价于接管后台。
	PermAccountRead  = "accounts:read"
	PermAccountWrite = "accounts:write"
)

var allPermissions = []string{
	PermStatsRead, PermStatsAggregate, PermReviewRead, PermReviewDecide,
	PermReportRead, PermReportResolve, PermUserRead, PermUserSanction,
	PermTopicRead, PermTopicWrite, PermConnectorRead, PermConnectorWrite,
	PermSettingsRead, PermSettingsWrite, PermAuditRead, PermDeliveryAdmin,
	PermAccountRead, PermAccountWrite,
}

// rolePermissions is the server-side RBAC matrix (default deny).
var rolePermissions = map[string][]string{
	"super_admin": allPermissions,
	"reviewer": {
		PermStatsRead, PermReviewRead, PermReviewDecide,
		PermReportRead, PermReportResolve, PermUserRead, PermTopicRead,
	},
	"operator": {
		PermStatsRead, PermStatsAggregate, PermReviewRead,
		PermReportRead, PermUserRead, PermUserSanction,
		PermTopicRead, PermTopicWrite, PermConnectorRead, PermConnectorWrite,
		PermSettingsRead, PermSettingsWrite, PermAuditRead, PermDeliveryAdmin,
	},
	"readonly": {
		PermStatsRead, PermReviewRead, PermReportRead, PermUserRead,
		PermTopicRead, PermConnectorRead, PermSettingsRead, PermAuditRead,
	},
}

func permissionsFor(role string) []string {
	perms, ok := rolePermissions[role]
	if !ok {
		return nil
	}
	return append([]string(nil), perms...)
}

func hasPermission(role, permission string) bool {
	for _, item := range rolePermissions[role] {
		if item == permission {
			return true
		}
	}
	return false
}

// Principal is the verified后台 identity; it is never derived from request bodies.
type Principal struct {
	AdminID   int64
	Email     string
	Role      string
	CSRF      string
	TokenHash []byte
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type sessionRecord struct {
	AdminID           int64     `json:"admin_id"`
	Email             string    `json:"email"`
	Role              string    `json:"role"`
	CSRF              string    `json:"csrf"`
	CreatedAt         time.Time `json:"created_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
}

// SessionStore keeps后台 active sessions in Redis; keys hold only a token hash.
type SessionStore struct {
	client *goredis.Client
	cfg    config.Config
}

func NewSessionStore(client *goredis.Client, cfg config.Config) *SessionStore {
	return &SessionStore{client: client, cfg: cfg}
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func sessionKey(hash []byte) string {
	return "admin_session:" + base64.RawURLEncoding.EncodeToString(hash)
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *SessionStore) Create(ctx context.Context, record sessionRecord) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	key := sessionKey(tokenHash(token))
	if err := s.client.Set(ctx, key, payload, s.cfg.SessionIdleTTL).Err(); err != nil {
		return "", fmt.Errorf("admin: store session: %w", err)
	}
	return token, nil
}

func (s *SessionStore) Load(ctx context.Context, token string) (sessionRecord, error) {
	var record sessionRecord
	if token == "" {
		return record, ErrUnauthorized
	}
	key := sessionKey(tokenHash(token))
	raw, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return record, ErrUnauthorized
	}
	if err != nil {
		return record, fmt.Errorf("admin: read session: %w", err)
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, ErrUnauthorized
	}
	if time.Now().UTC().After(record.AbsoluteExpiresAt) {
		_ = s.client.Del(ctx, key).Err()
		return record, ErrUnauthorized
	}
	// Idle renewal never extends the absolute lifetime (§11.4).
	_ = s.client.Expire(ctx, key, s.cfg.SessionIdleTTL).Err()
	return record, nil
}

func (s *SessionStore) Destroy(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.client.Del(ctx, sessionKey(tokenHash(token))).Err()
}

func (s *SessionStore) LoginAllowed(ctx context.Context, ip string) (bool, error) {
	key := "admin_login_fail:" + ip
	count, err := s.client.Get(ctx, key).Int64()
	if errors.Is(err, goredis.Nil) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return count < 10, nil
}

// RevokeUserSessions invalidates every active account session on ban (§4.7).
// The account module stores sessions under "yejie:session:" with user_id in JSON.
func (s *SessionStore) RevokeUserSessions(ctx context.Context, userID int64) error {
	var cursor uint64
	var keys []string
	for {
		batch, next, err := s.client.Scan(ctx, cursor, "yejie:session:*", 200).Result()
		if err != nil {
			return err
		}
		for _, key := range batch {
			raw, err := s.client.Get(ctx, key).Bytes()
			if err != nil {
				continue
			}
			var record struct {
				UserID int64 `json:"user_id"`
			}
			if json.Unmarshal(raw, &record) == nil && record.UserID == userID {
				keys = append(keys, key)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) > 0 {
		return s.client.Del(ctx, keys...).Err()
	}
	return nil
}

// RevokeAdminSessions 吊销某个后台账号的全部会话；except 非空时保留该 token，
// 用于「改自己的密码后保留当前会话」。改角色/停用/重置口令都必须调用。
func (s *SessionStore) RevokeAdminSessions(ctx context.Context, adminID int64, except []byte) (int, error) {
	var cursor uint64
	var keys []string
	keep := ""
	if len(except) > 0 {
		keep = sessionKey(except)
	}
	for {
		batch, next, err := s.client.Scan(ctx, cursor, "admin_session:*", 200).Result()
		if err != nil {
			return 0, err
		}
		for _, key := range batch {
			if key == keep {
				continue
			}
			raw, err := s.client.Get(ctx, key).Bytes()
			if err != nil {
				continue
			}
			var record sessionRecord
			if json.Unmarshal(raw, &record) == nil && record.AdminID == adminID {
				keys = append(keys, key)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return 0, nil
	}
	return len(keys), s.client.Del(ctx, keys...).Err()
}

func (s *SessionStore) NoteLoginFailure(ctx context.Context, ip string) {
	key := "admin_login_fail:" + ip
	_, _ = s.client.Incr(ctx, key).Result()
	_ = s.client.Expire(ctx, key, 15*time.Minute).Err()
}

func (s *SessionStore) ClearLoginFailures(ctx context.Context, ip string) {
	_ = s.client.Del(ctx, "admin_login_fail:"+ip).Err()
}

// Handler wires all /api/v1/admin/* endpoints and the RBAC middleware.
type Handler struct {
	repo       *Repository
	sessions   *SessionStore
	cfg        config.Config
	log        *slog.Logger
	store      StoreResolver
	aggregator Aggregator
	// avatarSetter 由组装层注入（复用 settings 的随机头像写入逻辑）。
	avatarSetter AvatarSetter
}

// AvatarSetter 给指定用户设置一张随机头像；返回签名地址。
type AvatarSetter func(ctx context.Context, userID int64, seed string) (string, error)

// SetAvatarSetter 注入写入能力；S3 不可用时不注入，后台头像入口返回 503。
func (h *Handler) SetAvatarSetter(fn AvatarSetter) { h.avatarSetter = fn }

func NewHandler(repo *Repository, sessions *SessionStore, cfg config.Config, log *slog.Logger,
	store StoreResolver, aggregator Aggregator) *Handler {
	return &Handler{
		repo: repo, sessions: sessions, cfg: cfg, log: log,
		store: store, aggregator: aggregator,
	}
}

func (h *Handler) cookieName() string {
	if h.cfg.CookieSecure {
		return cookieProd
	}
	return cookieDev
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, ErrUnauthorized):
		httpserver.Error(w, http.StatusUnauthorized, "unauthorized", "请先登录后台", requestID)
	case errors.Is(err, ErrForbidden):
		httpserver.Error(w, http.StatusForbidden, "forbidden", "当前角色无此权限", requestID)
	case errors.Is(err, ErrBadLogin):
		httpserver.Error(w, http.StatusUnauthorized, "invalid_credentials", "账号或密码不正确", requestID)
	case errors.Is(err, ErrNotFound):
		httpserver.Error(w, http.StatusNotFound, "not_found", "对象不存在", requestID)
	case errors.Is(err, ErrConflict):
		httpserver.Error(w, http.StatusConflict, "conflict", "操作与当前状态冲突", requestID)
	case errors.Is(err, ErrInvalidInput):
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", err.Error(), requestID)
	case errors.Is(err, httpserver.ErrInvalidJSON):
		httpserver.Error(w, http.StatusBadRequest, "invalid_json", "请求体不合法", requestID)
	default:
		h.log.Error("admin request failed", "error", err, "path", r.URL.Path,
			"request_id", requestID)
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败", requestID)
	}
}

// RequireAdmin verifies the Redis-backed session and injects the principal.
func (h *Handler) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(h.cookieName())
		if err != nil {
			h.writeError(w, r, ErrUnauthorized)
			return
		}
		record, err := h.sessions.Load(r.Context(), cookie.Value)
		if err != nil {
			h.writeError(w, r, ErrUnauthorized)
			return
		}
		principal := Principal{
			AdminID: record.AdminID, Email: record.Email, Role: record.Role,
			CSRF: record.CSRF, TokenHash: tokenHash(cookie.Value),
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
	})
}

// RequirePermission enforces server-side RBAC (§11.5). write=true also requires CSRF.
func (h *Handler) RequirePermission(permission string, write bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return h.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, _ := principalFrom(r.Context())
			if !hasPermission(principal.Role, permission) {
				h.writeError(w, r, ErrForbidden)
				return
			}
			if write {
				provided := r.Header.Get(csrfHeader)
				if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(principal.CSRF)) != 1 {
					requestID := httpserver.RequestIDFromContext(r.Context())
					httpserver.Error(w, http.StatusForbidden, "csrf_failed", "CSRF 校验失败", requestID)
					return
				}
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func (h *Handler) wrap(permission string, write bool, fn func(http.ResponseWriter, *http.Request)) http.Handler {
	return h.RequirePermission(permission, write)(http.HandlerFunc(fn))
}

// wrapSelf 只校验登录态与 CSRF，不绑定具体权限：用于「修改自己的密码」这类
// 每个角色都该能做、但不需要额外 RBAC 的操作。
func (h *Handler) wrapSelf(fn func(http.ResponseWriter, *http.Request)) http.Handler {
	return h.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFrom(r.Context())
		if !ok {
			h.writeError(w, r, ErrUnauthorized)
			return
		}
		provided := r.Header.Get(csrfHeader)
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(principal.CSRF)) != 1 {
			requestID := httpserver.RequestIDFromContext(r.Context())
			httpserver.Error(w, http.StatusForbidden, "csrf_failed", "CSRF 校验失败", requestID)
			return
		}
		fn(w, r)
	}))
}

// clientIP never trusts client-supplied proxy headers (§11.6).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// audit writes a read-only record for every privileged write (§11.6).
func (h *Handler) audit(r *http.Request, action, objectType string, objectID any,
	before, after any) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		return
	}
	var ip any
	if value := clientIP(r); value != "" {
		if parsed := net.ParseIP(value); parsed != nil {
			ip = parsed.String()
		}
	}
	_ = h.repo.WriteAudit(r.Context(), principal.AdminID, principal.Role, action,
		objectType, fmt.Sprint(objectID), before, after, ip, r.UserAgent())
}

// verifyLoginPassword reuses the Argon2id verifier from the account module.
func verifyLoginPassword(hash, password string) (bool, error) {
	return auth.VerifyPassword(hash, password)
}
