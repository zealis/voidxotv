package admin

import (
	"net/http"

	"github.com/yejie/voidxotv/internal/modules/spam"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 垃圾信息控制（功能开关页）：开关与自定义敏感词、按需下载的那一份词库、测试框。
// 词库全文只留在服务端，接口只下发概况。

func (h *Handler) spamConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := spam.LoadConfig(r.Context(), h.repo.db)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"config": cfg.Public()})
}

func (h *Handler) updateSpamConfig(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled bool   `json:"enabled"`
		Words   string `json:"words"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	before, err := spam.LoadConfig(r.Context(), h.repo.db)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := principalFrom(r.Context())
	if _, err := spam.SaveConfig(r.Context(), h.repo.db, principal.AdminID,
		request.Enabled, request.Words); err != nil {
		h.writeError(w, r, err)
		return
	}
	// 立即生效，不等 30 秒轮询。
	spam.Reload(r.Context())
	after, err := spam.LoadConfig(r.Context(), h.repo.db)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "settings.spam", "system_setting", "spam",
		map[string]any{"enabled": before.Enabled, "custom_words": len(before.Words)},
		map[string]any{"enabled": after.Enabled, "custom_words": len(after.Words)})
	httpserver.JSON(w, http.StatusOK, map[string]any{"config": after.Public()})
}

func (h *Handler) downloadSpamLexicon(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	result, err := spam.SyncLexicon(r.Context(), h.repo.db, principal.AdminID)
	if err != nil {
		// 外部依赖失败是可操作提示，不是「操作失败」这种系统故障。
		httpserver.Error(w, http.StatusBadRequest, "lexicon_download_failed", err.Error(),
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	h.audit(r, "settings.spam.lexicon", "system_setting", "spam", nil,
		map[string]any{"name": result.Name, "words": result.Words,
			"bytes": result.Bytes, "changed": result.Changed})
	cfg, err := spam.LoadConfig(r.Context(), h.repo.db)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"config": cfg.Public(), "downloaded": result,
	})
}

func (h *Handler) testSpam(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Text string `json:"text"`
	}
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	hits, enabled := spam.Inspect(r.Context(), request.Text)
	if hits == nil {
		hits = []string{}
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"enabled": enabled, "blocked": len(hits) > 0, "hits": hits,
	})
}
