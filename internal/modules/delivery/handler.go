package delivery

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// Handler exposes the internal Delivery management API (开发技术规范 §10.7).
// It is only reachable from internal services / administrators via a shared token;
// M4 will bind the same endpoints to admin RBAC.
type Handler struct {
	repo *Repository
	cfg  config.Config
	log  *slog.Logger
}

func NewHandler(repo *Repository, cfg config.Config, log *slog.Logger) *Handler {
	return &Handler{repo: repo, cfg: cfg, log: log}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/delivery/regenerate", h.guard(h.regenerate))
	mux.HandleFunc("GET /internal/delivery/jobs", h.guard(h.jobs))
	mux.HandleFunc("GET /internal/delivery/jobs/{id}", h.guard(h.job))
	mux.HandleFunc("GET /internal/delivery/pages", h.guard(h.page))
	mux.HandleFunc("GET /internal/delivery/pages/{key...}", h.guard(h.page))
	mux.HandleFunc("POST /internal/delivery/jobs/{id}/retry", h.guard(h.retry))
	mux.HandleFunc("POST /internal/delivery/jobs/{id}/cancel", h.guard(h.cancel))
	mux.HandleFunc("GET /internal/delivery/stats", h.guard(h.stats))
}

func (h *Handler) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Internal-Token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(h.cfg.DeliveryToken)) != 1 {
			httpserver.Error(w, http.StatusUnauthorized, "unauthorized", "internal token required",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		next(w, r)
	}
}

type regenerateRequest struct {
	PageKeys []string `json:"page_keys"`
	Priority int      `json:"priority"`
}

func (h *Handler) regenerate(w http.ResponseWriter, r *http.Request) {
	var request regenerateRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.badRequest(w, r)
		return
	}
	if len(request.PageKeys) == 0 || len(request.PageKeys) > 100 {
		h.badRequest(w, r)
		return
	}
	created, err := h.repo.Enqueue(r.Context(), request.PageKeys, request.Priority,
		h.cfg.DeliveryMaxAttempts, h.cfg.DeliveryDebounce)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusAccepted, map[string]any{"created": created, "requested": request.PageKeys})
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.repo.ListJobs(r.Context(), limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.badRequest(w, r)
		return
	}
	item, err := h.repo.GetJob(r.Context(), id)
	if err != nil {
		h.notFound(w, r)
		return
	}
	httpserver.JSON(w, http.StatusOK, item)
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("page_key")
	if key == "" {
		key = r.PathValue("key")
		if key != "" && !strings.HasPrefix(key, "/") && key != "sitemap:all" {
			key = "/" + key
		}
	}
	if key == "" {
		h.badRequest(w, r)
		return
	}
	page, err := h.repo.GetPage(r.Context(), key)
	if err != nil {
		h.notFound(w, r)
		return
	}
	httpserver.JSON(w, http.StatusOK, page)
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.badRequest(w, r)
		return
	}
	if err := h.repo.RetryJob(r.Context(), id); err != nil {
		h.notFound(w, r)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"id": id, "status": "retrying"})
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.badRequest(w, r)
		return
	}
	if err := h.repo.CancelJob(r.Context(), id); err != nil {
		h.notFound(w, r)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"id": id, "status": "cancelled"})
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.repo.Stats(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"stats": stats, "checked_at": time.Now().UTC()})
}

func (h *Handler) badRequest(w http.ResponseWriter, r *http.Request) {
	httpserver.Error(w, http.StatusBadRequest, "invalid_input", "invalid request",
		httpserver.RequestIDFromContext(r.Context()))
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	httpserver.Error(w, http.StatusNotFound, "not_found", "delivery object not found",
		httpserver.RequestIDFromContext(r.Context()))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("delivery admin error", "error", err, "path", r.URL.Path)
	if errors.Is(err, ErrNotFound) {
		h.notFound(w, r)
		return
	}
	httpserver.Error(w, http.StatusInternalServerError, "internal_error", "unexpected server error",
		httpserver.RequestIDFromContext(r.Context()))
}
