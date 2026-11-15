package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 敏感操作（删除作品 / 评论 / 图片 / 头像）的二次确认：
// 重新输入登录密码，换一个 5 分钟内有效的确认令牌。令牌与会话分开存，
// 会话被劫持时攻击者仍然不知道密码，删不掉东西。
const (
	stepUpTTL    = 5 * time.Minute
	stepUpHeader = "X-Step-Up-Token"
	stepUpScope  = "user"
)

// VerifyPassword 只校验当前登录用户的密码，不改变会话状态。
func (s *Service) VerifyPassword(ctx context.Context, userID int64, password string) (bool, error) {
	if password == "" {
		return false, nil
	}
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return VerifyPassword(user.PasswordHash, password)
}

// IssueStepUp 发放确认令牌；按用户隔离，不能跨账号使用。
func (s *Service) IssueStepUp(ctx context.Context, userID int64) (string, time.Duration, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := s.sessions.client.Set(ctx, stepUpKey(userID, token), "1", stepUpTTL).Err(); err != nil {
		return "", 0, fmt.Errorf("auth: issue step-up: %w", err)
	}
	return token, stepUpTTL, nil
}

func (s *Service) CheckStepUp(ctx context.Context, userID int64, token string) bool {
	if token == "" {
		return false
	}
	exists, err := s.sessions.client.Exists(ctx, stepUpKey(userID, token)).Result()
	return err == nil && exists == 1
}

func stepUpKey(userID int64, token string) string {
	return fmt.Sprintf("yejie:stepup:%s:%d:%s", stepUpScope, userID, token)
}

// RequireStepUp 是敏感写接口的统一闸门：没有有效令牌就返回
// 403 step_up_required，前端据此弹密码框，拿到令牌后自动重试原请求。
func (h *Handler) RequireStepUp(w http.ResponseWriter, r *http.Request) bool {
	principal, ok := PrincipalFromContext(r.Context())
	if ok && h.svc.CheckStepUp(r.Context(), principal.UserID, r.Header.Get(stepUpHeader)) {
		return true
	}
	httpserver.Error(w, http.StatusForbidden, "step_up_required", "请重新输入密码确认",
		httpserver.RequestIDFromContext(r.Context()))
	return false
}

// stepUp 校验密码并发放令牌。失败只说明「密码不正确」，不区分账号是否存在。
func (h *Handler) stepUp(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpserver.Error(w, http.StatusUnauthorized, "unauthorized", "请先登录",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		httpserver.Error(w, http.StatusBadRequest, "invalid_json", "请求体不合法",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	matched, err := h.svc.VerifyPassword(r.Context(), principal.UserID, request.Password)
	if err != nil {
		h.log.Error("step-up verify failed", "error", err)
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if !matched {
		httpserver.Error(w, http.StatusUnauthorized, "invalid_credentials", "密码不正确",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	token, ttl, err := h.svc.IssueStepUp(r.Context(), principal.UserID)
	if err != nil {
		h.log.Error("step-up issue failed", "error", err)
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"token": token, "expires_in": int(ttl.Seconds()),
	})
}
