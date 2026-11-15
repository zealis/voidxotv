package settings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/spam"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

// 头像限制 2 MiB：这是列表密度最高的图片，原图过大没有意义。
const maxAvatarBytes = 2 << 20

var (
	ErrBadAvatar   = errors.New("头像仅支持 PNG / JPEG / WebP，且不超过 2 MiB")
	ErrStorageDown = errors.New("对象存储暂不可用，请稍后再试")
)

type Handler struct {
	repo *Repository
	auth *auth.Handler
	log  *slog.Logger
	// 头像走对象存储；S3 在 cmd/api 里是可选启动的，用 setter 注入。
	store *objectstore.Store
	// 会话存储用于改密后吊销全部会话；用 setter 注入避免构造函数扩散。
	sessions *auth.SessionStore
}

func NewHandler(repo *Repository, authHandler *auth.Handler, log *slog.Logger) *Handler {
	return &Handler{repo: repo, auth: authHandler, log: log}
}

// SetImageStore 注入对象存储；未注入时头像接口返回 503，其余资料功能不受影响。
func (h *Handler) SetImageStore(store *objectstore.Store) { h.store = store }

// SetSessions 注入会话存储；未注入时改密仍生效，只是不吊销其他会话（仅测试环境）。
func (h *Handler) SetSessions(store *auth.SessionStore) { h.sessions = store }

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/me/settings", h.auth.RequireAuth(http.HandlerFunc(h.getSettings)))
	// 顺序固定为「先鉴权再校验 CSRF」：RequireCSRF 依赖上下文里的 principal。
	mux.Handle("PATCH /api/v1/me/settings",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.patchSettings))))
	mux.Handle("GET /api/v1/me/profile", h.auth.RequireAuth(http.HandlerFunc(h.getProfile)))
	mux.Handle("PATCH /api/v1/me/profile",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.patchProfile))))
	mux.Handle("POST /api/v1/me/password",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.changePassword))))
	mux.Handle("POST /api/v1/me/avatar",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.uploadAvatar))))
	// 随机头像 6 选 1：预览走同源 /api/v1/avatar/preview，选中后在这里落库。
	mux.Handle("POST /api/v1/me/avatar/random",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.randomAvatar))))
	mux.Handle("DELETE /api/v1/me/avatar",
		h.auth.RequireAuth(h.auth.RequireCSRF(http.HandlerFunc(h.deleteAvatar))))
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	value, err := h.repo.Load(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, value)
}

func (h *Handler) patchSettings(w http.ResponseWriter, r *http.Request) {
	var patch Patch
	if err := httpserver.DecodeJSON(r, &patch); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := validatePatch(patch); err != nil {
		h.fail(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	value, err := h.repo.Update(r.Context(), principal.UserID, patch)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, value)
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	profile, err := h.repo.Profile(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	profile.AvatarURL = h.avatarURL(r.Context(), profile.AvatarKey)
	httpserver.JSON(w, http.StatusOK, profile)
}

func (h *Handler) patchProfile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DisplayName *string `json:"display_name"`
		Bio         *string `json:"bio"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	if request.DisplayName != nil {
		value, err := normalizeDisplayName(*request.DisplayName)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		request.DisplayName = &value
	}
	if request.Bio != nil {
		value, err := normalizeBio(*request.Bio)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		request.Bio = &value
	}
	if request.DisplayName == nil && request.Bio == nil {
		h.fail(w, r, fmt.Errorf("%w：没有需要更新的字段", ErrInvalidInput))
		return
	}
	fields := make([]spam.Field, 0, 2)
	if request.DisplayName != nil {
		fields = append(fields, spam.Field{Label: "昵称", Value: *request.DisplayName})
	}
	if request.Bio != nil {
		fields = append(fields, spam.Field{Label: "个人简介", Value: *request.Bio})
	}
	if spam.Guard(w, r, fields...) {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	profile, err := h.repo.UpdateProfile(r.Context(), principal.UserID, request.DisplayName, request.Bio)
	if err == nil {
		profile.AvatarURL = h.avatarURL(r.Context(), profile.AvatarKey)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, profile)
}

// changePassword 是用户自助改密（§11.4）：改密成功后撤销该用户全部活跃会话，
// 所有已登录设备（含当前设备）回到登录页重新认证。
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	hash, err := h.repo.PasswordHash(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ok, err := auth.VerifyPassword(hash, request.CurrentPassword)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !ok {
		h.fail(w, r, fmt.Errorf("%w：当前密码不正确", ErrInvalidInput))
		return
	}
	if err := validateNewPassword(request.NewPassword, request.CurrentPassword); err != nil {
		h.fail(w, r, err)
		return
	}
	next, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.repo.UpdatePassword(r.Context(), principal.UserID, next); err != nil {
		h.fail(w, r, err)
		return
	}
	if h.sessions != nil {
		// Redis 会话是有效性的事实来源；审计记录保留在 user_sessions 表。
		if err := h.sessions.RevokeAll(r.Context(), principal.UserID); err != nil {
			// 吊销失败不能让改密白做：密码已更新，记录告警人工排查。
			h.log.Error("revoke sessions after password change failed",
				"error", err, "user_id", principal.UserID)
		}
	}
	h.log.Info("user password changed", "user_id", principal.UserID)
	httpserver.JSON(w, http.StatusOK, map[string]bool{"ok": true, "sessions_revoked": h.sessions != nil})
}

// ── 校验 ─────────────────────────────────────────────────────

func validatePatch(patch Patch) error {
	for name, value := range map[string]*int{
		"免打扰开始时间": patch.QuietStart,
		"免打扰结束时间": patch.QuietEnd,
	} {
		if value != nil && (*value < 0 || *value > 1439) {
			return fmt.Errorf("%w：%s必须在 0–1439 分钟之间", ErrInvalidInput, name)
		}
	}
	if patch.DefaultVisibility != nil && !ValidVisibility(*patch.DefaultVisibility) {
		return fmt.Errorf("%w：默认可见性只能是 public / friends / private", ErrInvalidInput)
	}
	if patch.Theme != nil && !ValidTheme(*patch.Theme) {
		return fmt.Errorf("%w：主题只能是 system / light / dark", ErrInvalidInput)
	}
	return nil
}

func normalizeDisplayName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if n := utf8.RuneCountInString(value); n == 0 || n > 24 {
		return "", fmt.Errorf("%w：昵称需为 1–24 个字符", ErrInvalidInput)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w：昵称不能包含控制字符", ErrInvalidInput)
		}
	}
	return value, nil
}

func normalizeBio(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 120 {
		return "", fmt.Errorf("%w：简介不能超过 120 个字符", ErrInvalidInput)
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' {
			return "", fmt.Errorf("%w：简介不能包含控制字符", ErrInvalidInput)
		}
	}
	return value, nil
}

func validateNewPassword(next, current string) error {
	if len(next) < 8 || len(next) > 72 {
		return fmt.Errorf("%w：新密码需为 8–72 个字符", ErrInvalidInput)
	}
	if strings.ContainsAny(next, " \t\r\n") {
		return fmt.Errorf("%w：新密码不能包含空白字符", ErrInvalidInput)
	}
	if next == current {
		return fmt.Errorf("%w：新密码不能与当前密码相同", ErrInvalidInput)
	}
	return nil
}

// avatarFormat 用魔数判定真实类型：客户端传的 Content-Type 不可信。
func avatarFormat(contentType string) (ext, mime string, ok bool) {
	switch contentType {
	case "image/png":
		return "png", "image/png", true
	case "image/jpeg":
		return "jpg", "image/jpeg", true
	case "image/webp":
		return "webp", "image/webp", true
	}
	return "", "", false
}

func (h *Handler) avatarURL(ctx context.Context, key string) string {
	if key == "" || h.store == nil {
		return ""
	}
	url, err := h.store.MediaURL(ctx, key)
	if err != nil {
		return ""
	}
	return url
}

// POST /api/v1/me/avatar —— multipart/form-data，字段名 image。
func (h *Handler) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		h.fail(w, r, ErrStorageDown)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAvatarBytes); err != nil {
		h.fail(w, r, ErrBadAvatar)
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		h.fail(w, r, ErrBadAvatar)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAvatarBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxAvatarBytes {
		h.fail(w, r, ErrBadAvatar)
		return
	}
	ext, mime, ok := avatarFormat(http.DetectContentType(data))
	if !ok {
		h.fail(w, r, ErrBadAvatar)
		return
	}
	// 上传时缩放到 ≤512px 长边（§11.2）：展示位最大 96px×2，原图直存浪费带宽。
	// WebP 缩放后转存为 PNG，键与 MIME 随之改变。
	data, mime, ext, resized := scaleAvatar(data, mime, ext, h.log)
	ctx := r.Context()
	// 版本化键：覆盖头像后旧图不会被 CDN 缓存命中。
	key := fmt.Sprintf("avatars/%d/%d.%s", principal.UserID, time.Now().UnixNano(), ext)
	if err := h.store.PutBytes(ctx, key, data, mime, "public, max-age=31536000, immutable"); err != nil {
		h.log.Warn("settings: put avatar", "error", err)
		h.fail(w, r, ErrStorageDown)
		return
	}
	old, err := h.repo.AvatarKey(ctx, principal.UserID)
	if err == nil {
		err = h.repo.SetAvatar(ctx, principal.UserID, key)
	}
	if err != nil {
		// 写库失败要把刚写入的对象清掉，避免留下无人引用的孤儿。
		_ = h.store.Remove(ctx, key)
		h.fail(w, r, err)
		return
	}
	if old != "" {
		_ = h.store.Remove(ctx, old)
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"avatar_url": h.avatarURL(ctx, key), "resized": resized,
	})
}

// DELETE /api/v1/me/avatar —— 清空头像，前端回落到占位图。
func (h *Handler) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	if !h.auth.RequireStepUp(w, r) {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	ctx := r.Context()
	old, err := h.repo.AvatarKey(ctx, principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.repo.SetAvatar(ctx, principal.UserID, ""); err != nil {
		h.fail(w, r, err)
		return
	}
	if old != "" && h.store != nil {
		_ = h.store.Remove(ctx, old)
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"avatar_url": ""})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, httpserver.ErrInvalidJSON):
		httpserver.Error(w, http.StatusBadRequest, "invalid_json", "请求体不合法", requestID)
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrBadAvatar):
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", err.Error(), requestID)
	case errors.Is(err, ErrStorageDown):
		httpserver.Error(w, http.StatusServiceUnavailable, "storage_unavailable", err.Error(), requestID)
	case errors.Is(err, pgx.ErrNoRows):
		httpserver.Error(w, http.StatusNotFound, "not_found", "账号不存在", requestID)
	default:
		h.log.Error("settings failed", "error", err, "path", r.URL.Path)
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败", requestID)
	}
}
