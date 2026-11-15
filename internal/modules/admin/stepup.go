package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/totp"
)

// 后台敏感操作（处置 / 下架 / 封禁 / 合并 / 停用 / 重置密码 / 重算榜单）的
// 二次确认：重新输入后台密码，启用了 TOTP 的账号还要动态验证码；
// 通过后发 5 分钟令牌，避免连续处置时反复输入。
const (
	stepUpTTL    = 5 * time.Minute
	stepUpHeader = "X-Step-Up-Token"
)

type adminCredentials struct {
	PasswordHash string
	TOTPSecret   string
}

func (r *Repository) AdminCredentials(ctx context.Context, id int64) (adminCredentials, error) {
	var creds adminCredentials
	err := r.db.QueryRow(ctx, `
		SELECT password_hash, COALESCE(totp_secret, '') FROM admin_users WHERE id = $1`,
		id).Scan(&creds.PasswordHash, &creds.TOTPSecret)
	return creds, err
}

func stepUpKey(adminID int64, token string) string {
	return fmt.Sprintf("yejie:stepup:admin:%d:%s", adminID, token)
}

func (h *Handler) issueStepUp(ctx context.Context, adminID int64) (string, time.Duration, error) {
	token, err := newToken()
	if err != nil {
		return "", 0, err
	}
	if err := h.sessions.client.Set(ctx, stepUpKey(adminID, token), "1", stepUpTTL).Err(); err != nil {
		return "", 0, fmt.Errorf("admin: issue step-up: %w", err)
	}
	return token, stepUpTTL, nil
}

func (h *Handler) checkStepUp(ctx context.Context, adminID int64, token string) bool {
	if token == "" {
		return false
	}
	exists, err := h.sessions.client.Exists(ctx, stepUpKey(adminID, token)).Result()
	return err == nil && exists == 1
}

// requireStepUp 是后台敏感接口的统一闸门；失败返回 403 step_up_required，
// 前端据此弹出密码 / 验证码确认框并自动重试。
func (h *Handler) requireStepUp(w http.ResponseWriter, r *http.Request) bool {
	principal, ok := principalFrom(r.Context())
	if ok && h.checkStepUp(r.Context(), principal.AdminID, r.Header.Get(stepUpHeader)) {
		return true
	}
	httpserver.Error(w, http.StatusForbidden, "step_up_required", "请重新输入密码确认",
		httpserver.RequestIDFromContext(r.Context()))
	return false
}

func (h *Handler) stepUp(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	var request struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	creds, err := h.repo.AdminCredentials(r.Context(), principal.AdminID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	matched, err := verifyLoginPassword(creds.PasswordHash, request.Password)
	if err != nil || !matched {
		httpserver.Error(w, http.StatusUnauthorized, "invalid_credentials", "密码不正确",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if creds.TOTPSecret != "" {
		code := strings.TrimSpace(request.TOTPCode)
		if code == "" {
			httpserver.Error(w, http.StatusBadRequest, "totp_required", "请输入动态验证码",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		if !totp.Validate(creds.TOTPSecret, code, time.Now()) {
			httpserver.Error(w, http.StatusUnauthorized, "invalid_totp", "动态验证码不正确",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
	}
	token, ttl, err := h.issueStepUp(r.Context(), principal.AdminID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "auth.stepup", "admin", principal.AdminID, nil,
		map[string]any{"totp": creds.TOTPSecret != ""})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"token": token, "expires_in": int(ttl.Seconds()),
	})
}
