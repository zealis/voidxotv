package admin

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 后台账号与口令（§4.7 / §11.5）：账号体系与前台用户完全分离，
// 口令只以 Argon2id 哈希落库，任何接口都不回传口令材料。

const (
	minAdminPassword = 8
	maxAdminPassword = 72
)

var (
	adminUsernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
	adminRoleSet         = []string{"super_admin", "reviewer", "operator", "readonly"}
	adminStatusSet       = []string{"active", "disabled"}
)

func validRole(role string) bool {
	for _, item := range adminRoleSet {
		if item == role {
			return true
		}
	}
	return false
}

func validStatus(status string) bool {
	for _, item := range adminStatusSet {
		if item == status {
			return true
		}
	}
	return false
}

// validateAdminPassword 只做离线可校验的规则，强度策略由部署侧再叠加。
func validateAdminPassword(password, username string) error {
	if utf8.RuneCountInString(password) < minAdminPassword {
		return fmt.Errorf("%w: 密码至少 %d 位", ErrInvalidInput, minAdminPassword)
	}
	if len(password) > maxAdminPassword {
		return fmt.Errorf("%w: 密码不能超过 %d 字节", ErrInvalidInput, maxAdminPassword)
	}
	if strings.TrimSpace(password) != password {
		return fmt.Errorf("%w: 密码首尾不能有空格", ErrInvalidInput)
	}
	if strings.EqualFold(password, username) {
		return fmt.Errorf("%w: 密码不能与账号相同", ErrInvalidInput)
	}
	return nil
}

type createAccountRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

type accountRoleRequest struct {
	Role string `json:"role"`
}

type accountStatusRequest struct {
	Status string `json:"status"`
}

type accountPasswordRequest struct {
	Password string `json:"password"`
}

type ownPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAdmins(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "count": len(items), "roles": adminRoleSet,
	})
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var request createAccountRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	username := strings.ToLower(strings.TrimSpace(request.Username))
	if !adminUsernamePattern.MatchString(username) {
		h.writeError(w, r, fmt.Errorf(
			"%w: 账号需 3–32 位小写字母/数字/下划线，且以字母开头", ErrInvalidInput))
		return
	}
	if !validRole(request.Role) {
		h.writeError(w, r, fmt.Errorf("%w: 角色不合法", ErrInvalidInput))
		return
	}
	if err := validateAdminPassword(request.Password, username); err != nil {
		h.writeError(w, r, err)
		return
	}
	hash, err := auth.HashPassword(request.Password)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	item, err := h.repo.CreateAdmin(r.Context(), username, request.Role, hash)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "admin.create", "admin", item.ID, nil, map[string]any{
		"username": item.Username, "role": item.Role,
	})
	httpserver.JSON(w, http.StatusCreated, item)
}

func (h *Handler) setAccountRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request accountRoleRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	if !validRole(request.Role) {
		h.writeError(w, r, fmt.Errorf("%w: 角色不合法", ErrInvalidInput))
		return
	}
	principal, _ := principalFrom(r.Context())
	if principal.AdminID == id {
		httpserver.Error(w, http.StatusConflict, "self_protected",
			"不能调整自己的角色，请让其他超级管理员操作", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	target, err := h.repo.FindAdminByID(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if target.Role == request.Role {
		h.writeError(w, r, fmt.Errorf("%w: 角色没有变化", ErrInvalidInput))
		return
	}
	if target.Role == "super_admin" && target.Status == "active" {
		remaining, err := h.repo.CountActiveSuperAdmins(r.Context(), id)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		if remaining == 0 {
			httpserver.Error(w, http.StatusConflict, "last_super_admin",
				"至少保留一个可用的超级管理员", httpserver.RequestIDFromContext(r.Context()))
			return
		}
	}
	if err := h.repo.UpdateAdminRole(r.Context(), id, request.Role); err != nil {
		h.writeError(w, r, err)
		return
	}
	// 角色写在会话里，改完必须让旧会话失效，否则权限要等下次登录才生效。
	revoked, err := h.sessions.RevokeAdminSessions(r.Context(), id, nil)
	if err != nil {
		h.log.Warn("revoke admin sessions failed", "admin_id", id, "error", err)
	}
	h.audit(r, "admin.role", "admin", id, map[string]any{"role": target.Role},
		map[string]any{"role": request.Role, "revoked_sessions": revoked})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": id, "username": target.Username, "role": request.Role,
		"revoked_sessions": revoked,
	})
}

func (h *Handler) setAccountStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request accountStatusRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	if !validStatus(request.Status) {
		h.writeError(w, r, fmt.Errorf("%w: 状态不合法", ErrInvalidInput))
		return
	}
	principal, _ := principalFrom(r.Context())
	if principal.AdminID == id && request.Status != "active" {
		httpserver.Error(w, http.StatusConflict, "self_protected",
			"不能停用自己的账号", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	target, err := h.repo.FindAdminByID(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if target.Status == request.Status {
		h.writeError(w, r, fmt.Errorf("%w: 状态没有变化", ErrInvalidInput))
		return
	}
	if target.Role == "super_admin" && request.Status != "active" {
		remaining, err := h.repo.CountActiveSuperAdmins(r.Context(), id)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		if remaining == 0 {
			httpserver.Error(w, http.StatusConflict, "last_super_admin",
				"至少保留一个可用的超级管理员", httpserver.RequestIDFromContext(r.Context()))
			return
		}
	}
	if err := h.repo.UpdateAdminStatus(r.Context(), id, request.Status); err != nil {
		h.writeError(w, r, err)
		return
	}
	revoked, err := h.sessions.RevokeAdminSessions(r.Context(), id, nil)
	if err != nil {
		h.log.Warn("revoke admin sessions failed", "admin_id", id, "error", err)
	}
	h.audit(r, "admin.status", "admin", id, map[string]any{"status": target.Status},
		map[string]any{"status": request.Status, "revoked_sessions": revoked})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": id, "username": target.Username, "status": request.Status,
		"revoked_sessions": revoked,
	})
}

func (h *Handler) resetAccountPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request accountPasswordRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	target, err := h.repo.FindAdminByID(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := validateAdminPassword(request.Password, target.Username); err != nil {
		h.writeError(w, r, err)
		return
	}
	hash, err := auth.HashPassword(request.Password)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.repo.UpdateAdminPassword(r.Context(), id, hash); err != nil {
		h.writeError(w, r, err)
		return
	}
	revoked, err := h.sessions.RevokeAdminSessions(r.Context(), id, nil)
	if err != nil {
		h.log.Warn("revoke admin sessions failed", "admin_id", id, "error", err)
	}
	// 审计只记录「重置过」，不落任何口令片段。
	h.audit(r, "admin.password.reset", "admin", id, nil, map[string]any{
		"revoked_sessions": revoked,
	})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": id, "username": target.Username, "reset": true, "revoked_sessions": revoked,
	})
}

// changeOwnPassword 允许任何已登录账号改自己的口令：校验旧口令、吊销其它会话、
// 保留当前会话，避免刚改完就被踢出去。
func (h *Handler) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var request ownPasswordRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	principal, ok := principalFrom(r.Context())
	if !ok {
		h.writeError(w, r, ErrUnauthorized)
		return
	}
	account, err := h.repo.FindAdminByID(r.Context(), principal.AdminID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	matched, err := auth.VerifyPassword(account.PasswordHash, request.CurrentPassword)
	if err != nil || !matched {
		httpserver.Error(w, http.StatusBadRequest, "invalid_credentials",
			"当前密码不正确", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	if err := validateAdminPassword(request.NewPassword, account.Username); err != nil {
		h.writeError(w, r, err)
		return
	}
	if same, err := auth.VerifyPassword(account.PasswordHash, request.NewPassword); err == nil && same {
		h.writeError(w, r, fmt.Errorf("%w: 新密码不能与当前密码相同", ErrInvalidInput))
		return
	}
	hash, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.repo.UpdateAdminPassword(r.Context(), account.ID, hash); err != nil {
		h.writeError(w, r, err)
		return
	}
	revoked, err := h.sessions.RevokeAdminSessions(r.Context(), account.ID, principal.TokenHash)
	if err != nil {
		h.log.Warn("revoke admin sessions failed", "admin_id", account.ID, "error", err)
	}
	h.audit(r, "admin.password.change", "admin", account.ID, nil, map[string]any{
		"revoked_sessions": revoked,
	})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"id": account.ID, "changed": true, "revoked_sessions": revoked,
	})
}
