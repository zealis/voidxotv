package admin

// 后台「用户管理 → 设置头像」：从 6 张随机 DiceBear Micah 头像里选一张，
// 复用 settings 的下载 / 存储 / 写库逻辑，避免两处各实现一遍。

import (
	"net/http"
	"strconv"

	"github.com/yejie/voidxotv/internal/platform/avatar"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type setUserAvatarRequest struct {
	Seed string `json:"seed"`
}

func (h *Handler) setUserAvatar(w http.ResponseWriter, r *http.Request) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", "用户编号不合法", requestID)
		return
	}
	if h.avatarSetter == nil {
		httpserver.Error(w, http.StatusServiceUnavailable, "storage_unavailable", "头像服务暂不可用", requestID)
		return
	}
	var request setUserAvatarRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		httpserver.Error(w, http.StatusBadRequest, "invalid_json", "请求体不合法", requestID)
		return
	}
	if !avatar.ValidSeed(request.Seed) {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", "请先选择一张头像", requestID)
		return
	}
	url, err := h.avatarSetter(r.Context(), userID, request.Seed)
	if err != nil {
		h.log.Warn("admin: set user avatar failed", "user_id", userID, "error", err)
		httpserver.Error(w, http.StatusServiceUnavailable, "storage_unavailable", "头像保存失败，请稍后再试", requestID)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"avatar_url": url})
}
