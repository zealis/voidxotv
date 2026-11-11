package admin

import (
	"net/http"
	"strconv"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type categoryStatusRequest struct {
	Status string `json:"status"`
}

type categoryAssignRequest struct {
	ContentID  int64 `json:"content_id"`
	CategoryID int64 `json:"category_id"`
	Attach     bool  `json:"attach"`
}

func (h *Handler) categories(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.Categories(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

// upsertCategory creates a new category (id=0) or updates an existing one.
func (h *Handler) upsertCategory(w http.ResponseWriter, r *http.Request) {
	var request CategoryInput
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.repo.UpsertCategory(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	action := "category.create"
	if request.ID > 0 {
		action = "category.update"
	}
	h.audit(r, action, "category", item.ID, nil, item)
	httpserver.JSON(w, http.StatusOK, item)
}

func (h *Handler) setCategoryStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	var request categoryStatusRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.repo.SetCategoryStatus(r.Context(), id, request.Status)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "category.status", "category", id, nil, item)
	httpserver.JSON(w, http.StatusOK, item)
}

func (h *Handler) assignCategory(w http.ResponseWriter, r *http.Request) {
	var request categoryAssignRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	if request.ContentID <= 0 || request.CategoryID <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	previous, err := h.repo.AssignCategory(r.Context(), request.ContentID,
		request.CategoryID, request.Attach)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.audit(r, "category.assign", "content", request.ContentID, nil, map[string]any{
		"category_id": request.CategoryID, "attach": request.Attach,
	})
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"content_id": request.ContentID, "category_id": request.CategoryID,
		"attached": request.Attach, "previous_categories": previous,
	})
}

func (h *Handler) contentCategories(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	ids, err := h.repo.ContentCategoryIDs(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"content_id": id, "category_ids": ids, "count": len(ids),
	})
}
