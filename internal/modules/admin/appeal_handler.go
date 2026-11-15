package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type appealDecisionRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note"`
}

func (h *Handler) appeals(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := parseInt(r.URL.Query().Get("limit"), 50, 1, 100)
	items, err := h.repo.AppealQueue(r.Context(), status, limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

// decideAppeal accepts (restore content) or rejects one appeal.
func (h *Handler) decideAppeal(w http.ResponseWriter, r *http.Request) {
	if !h.requireStepUp(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request appealDecisionRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	// 驳回申诉必须说明理由，便于作者理解结论（与审核动作同口径）。
	if !request.Approve && strings.TrimSpace(request.Note) == "" {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	principal, _ := principalFrom(r.Context())
	result, err := h.repo.DecideAppeal(r.Context(), id, principal.AdminID,
		request.Approve, request.Note)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	action := "appeal.reject"
	if request.Approve {
		action = "appeal.accept"
	}
	h.audit(r, action, "content_appeal", id, nil, result)
	httpserver.JSON(w, http.StatusOK, result)
}
