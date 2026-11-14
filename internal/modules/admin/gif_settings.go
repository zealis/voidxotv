package admin

import (
	"fmt"
	"net/http"

	"github.com/yejie/voidxotv/internal/modules/gifs"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 功能开关（§4.7）：目前只有「GIF 表情包」一项。
//
// 为什么单独一组接口而不是塞进 /admin/settings：那一份是脱敏的运行诊断快照，
// 谁能读谁就能读，而这里的 key 是能直接消耗配额的凭据，读写权限必须分开。

// gifConfig 回读当前配置。ready=false 表示选了图源但密钥没填全，
// 前端据此显示「未启用」而不是让人以为已经在用。
func (h *Handler) gifConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := gifs.LoadConfig(r.Context(), h.repo.db)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"config": cfg, "ready": cfg.Ready(),
	})
}

func (h *Handler) updateGifConfig(w http.ResponseWriter, r *http.Request) {
	var request gifs.Config
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	// 选了图源却不填凭据，存下去只是个「看起来启用了」的空壳，不如当场拦下。
	if request.Provider == gifs.ProviderApihz &&
		(request.ApihzID == "" || request.ApihzKey == "") {
		h.writeError(w, r, fmt.Errorf("%w: 选择 API 盒子需同时填写开发者 ID 与 KEY", ErrInvalidInput))
		return
	}
	if request.Provider == gifs.ProviderKlipy && request.KlipyKey == "" {
		h.writeError(w, r, fmt.Errorf("%w: 选择 KLIPY 需填写 App Key", ErrInvalidInput))
		return
	}
	before, err := gifs.LoadConfig(r.Context(), h.repo.db)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := principalFrom(r.Context())
	saved, err := gifs.SaveConfig(r.Context(), h.repo.db, principal.AdminID, request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 审计只落非敏感字段：key 一旦进审计表就等于换了地方存明文。
	h.audit(r, "settings.gif", "system_setting", "gif",
		map[string]any{"provider": before.Provider, "line": before.ApihzLine,
			"id": before.ApihzID, "limit": before.ApihzLimit, "cache_days": before.CacheDays,
			"klipy_filter": before.KlipyFilter, "klipy_locale": before.KlipyLocale,
			"klipy_limit": before.KlipyLimit, "klipy_cache_days": before.KlipyCacheDays},
		map[string]any{"provider": saved.Provider, "line": saved.ApihzLine,
			"id": saved.ApihzID, "limit": saved.ApihzLimit, "cache_days": saved.CacheDays,
			"klipy_filter": saved.KlipyFilter, "klipy_locale": saved.KlipyLocale,
			"klipy_limit": saved.KlipyLimit, "klipy_cache_days": saved.KlipyCacheDays,
			"apihz_key_set": saved.ApihzKey != "", "klipy_key_set": saved.KlipyKey != ""})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"config": saved, "ready": saved.Ready(),
	})
}
