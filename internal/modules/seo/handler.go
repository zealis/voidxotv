package seo

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

type Handler struct {
	repo *Repository
	log  *slog.Logger
}

func NewHandler(repo *Repository, log *slog.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/seo/pages", h.pages)
	mux.HandleFunc("GET /api/v1/seo/content/{slug}", h.content)
}

func (h *Handler) pages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	items, err := h.repo.List(r.Context(), cursor, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	next := int64(0)
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "count": len(items), "next_cursor": next,
	})
}

func (h *Handler) content(w http.ResponseWriter, r *http.Request) {
	page, err := h.repo.Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpserver.Error(w, http.StatusNotFound, "not_found", "content not indexable",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpserver.JSON(w, http.StatusOK, page)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("seo error", "error", err, "path", r.URL.Path)
	httpserver.Error(w, http.StatusInternalServerError, "internal_error", "unexpected server error",
		httpserver.RequestIDFromContext(r.Context()))
}
