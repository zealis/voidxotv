package content

import (
	"net/http"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type appealRequest struct {
	Reason string `json:"reason"`
}

// appeal records the author's appeal against a reject/take-down/limit decision.
func (h *Handler) appeal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var request appealRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	appeal, err := h.svc.Appeal(r.Context(), id, principal.UserID, request.Reason)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, appeal)
}

// appeals returns the appeal history visible to the author.
func (h *Handler) appeals(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	items, err := h.svc.ListAppeals(r.Context(), id, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

// myAppeals returns the latest appeal per content for the signed-in author.
func (h *Handler) myAppeals(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	items, err := h.svc.MyAppeals(r.Context(), principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}
