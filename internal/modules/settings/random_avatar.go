package settings

// 「随机头像 6 选 1」的写入侧：把选中的 seed 下载进自有对象存储并设为头像。
// 用户自己从设置页调用，后台用户管理通过 SetAvatarSetter 注入同一个方法，
// 避免两处各写一份「下载 / 存对象 / 写库 / 删旧图」的逻辑。

import (
	"context"
	"fmt"
	"net/http"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/avatar"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/imageproc"
)

// SetRandomAvatar 返回新的签名头像地址；任何失败都不会留下无人引用的对象。
func (h *Handler) SetRandomAvatar(ctx context.Context, userID int64, seed string) (string, error) {
	if h.store == nil {
		return "", ErrStorageDown
	}
	if !avatar.ValidSeed(seed) {
		return "", fmt.Errorf("%w: 头像标识不合法", ErrInvalidInput)
	}
	data, err := avatar.FetchPNG(ctx, seed)
	if err != nil {
		return "", fmt.Errorf("%w: 头像获取失败", ErrStorageDown)
	}
	// 随机头像与上传头像同口径：转成长边 ≤512 的 WebP 存一份，不留 PNG 原图。
	webp, ok := imageproc.MakeAvatar(data)
	if !ok {
		return "", fmt.Errorf("%w: 头像转码失败", ErrStorageDown)
	}
	key := avatar.Key(userID)
	if err := h.store.PutBytes(ctx, key, webp, "image/webp", avatar.CacheHeader); err != nil {
		return "", ErrStorageDown
	}
	old, err := h.repo.AvatarKey(ctx, userID)
	if err != nil {
		_ = h.store.Remove(ctx, key)
		return "", err
	}
	if err := h.repo.SetAvatar(ctx, userID, key); err != nil {
		_ = h.store.Remove(ctx, key)
		return "", err
	}
	if old != "" {
		_ = h.store.Remove(ctx, old)
	}
	return h.avatarURL(ctx, key), nil
}

type randomAvatarRequest struct {
	Seed string `json:"seed"`
}

func (h *Handler) randomAvatar(w http.ResponseWriter, r *http.Request) {
	var request randomAvatarRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.fail(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	url, err := h.SetRandomAvatar(r.Context(), principal.UserID, request.Seed)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"avatar_url": url})
}
