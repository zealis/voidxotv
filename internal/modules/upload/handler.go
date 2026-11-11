package upload

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
	"github.com/yejie/voidxotv/internal/platform/queue"
)

var allowedContentTypes = map[string]bool{
	"video/mp4": true, "video/quicktime": true, "video/webm": true,
	"video/x-matroska": true, "video/x-msvideo": true, "video/mpeg": true,
}

type Handler struct {
	repo     *Repository
	store    *objectstore.Store
	notifier *queue.Notifier
	auth     *auth.Handler
	cfg      config.Config
	log      *slog.Logger
}

func NewHandler(repo *Repository, store *objectstore.Store, notifier *queue.Notifier,
	authHandler *auth.Handler, cfg config.Config, log *slog.Logger) *Handler {
	return &Handler{repo: repo, store: store, notifier: notifier, auth: authHandler, cfg: cfg, log: log}
}

func (h *Handler) guarded(next http.HandlerFunc) http.Handler {
	return h.auth.RequireAuth(h.auth.RequireCSRF(next))
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/uploads", h.guarded(h.create))
	mux.Handle("POST /api/v1/uploads/{id}/complete", h.guarded(h.complete))
	mux.Handle("GET /api/v1/uploads/{id}", h.auth.RequireAuth(http.HandlerFunc(h.status)))
}

type createRequest struct {
	ContentType    string `json:"content_type"`
	ByteSize       int64  `json:"byte_size"`
	IdempotencyKey string `json:"idempotency_key"`
	// 发布页草稿自动保存（§6.1）：复用已存在的草稿，而不是再建一条空记录。
	ContentID int64 `json:"content_id,omitempty"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var request createRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !allowedContentTypes[request.ContentType] {
		h.writeError(w, r, ErrBadContentType)
		return
	}
	if request.ByteSize <= 0 || request.ByteSize > h.cfg.MaxUploadBytes {
		h.writeError(w, r, ErrTooLarge)
		return
	}
	partCount := int((request.ByteSize + objectstore.PartSize - 1) / objectstore.PartSize)
	if partCount < 1 || partCount > objectstore.MaxParts {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	if request.IdempotencyKey == "" {
		key, err := randomKey()
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		request.IdempotencyKey = key
	}

	row, replayErr := h.findByKey(r.Context(), principal.UserID, request.IdempotencyKey)
	if replayErr != nil {
		var err error
		objectKey, err := objectstore.SourceKey(principal.UserID)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		multipartID, err := h.store.NewMultipart(r.Context(), objectKey, request.ContentType)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		uploadJobID, contentID, _, err := h.repo.CreateTicket(r.Context(), principal.UserID,
			request.ContentID, request.IdempotencyKey, objectKey, multipartID,
			request.ContentType, request.ByteSize, partCount)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		row = ticketRow{
			uploadJobID: uploadJobID, contentID: contentID,
			objectKey: objectKey, multipartID: multipartID,
			declaredSize: request.ByteSize, partCount: partCount, status: "pending",
		}
	}

	ticket := Ticket{
		UploadJobID: row.uploadJobID, ContentID: row.contentID,
		ObjectKey: row.objectKey, UploadID: row.multipartID,
		PartSize:  objectstore.PartSize,
		ExpiresAt: time.Now().UTC().Add(objectstore.PartURLTTL),
	}
	for part := 1; part <= row.partCount; part++ {
		signedURL, err := h.store.PresignPart(r.Context(), row.objectKey, row.multipartID, part)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		ticket.Parts = append(ticket.Parts, PresignedPart{PartNumber: part, URL: signedURL})
	}
	httpserver.JSON(w, http.StatusCreated, ticket)
}

type ticketRow struct {
	uploadJobID  int64
	contentID    int64
	objectKey    string
	multipartID  string
	declaredSize int64
	partCount    int
	status       string
}

func (h *Handler) findByKey(ctx context.Context, ownerID int64, idemKey string) (ticketRow, error) {
	uploadJobID, contentID, objectKey, multipartID, declaredSize, partCount, status, err :=
		h.repo.FindByIdempotency(ctx, ownerID, idemKey)
	if err != nil {
		return ticketRow{}, err
	}
	return ticketRow{
		uploadJobID: uploadJobID, contentID: contentID, objectKey: objectKey,
		multipartID: multipartID, declaredSize: declaredSize, partCount: partCount, status: status,
	}, nil
}

type completeRequest struct {
	Parts []CompletedPart `json:"parts"`
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	var request completeRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	uploadJobID, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	_, objectKey, multipartID, declaredSize, partCount, _, err :=
		h.repo.Load(r.Context(), uploadJobID, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(request.Parts) != partCount || len(request.Parts) == 0 {
		h.writeError(w, r, ErrBadParts)
		return
	}
	parts := make([]minio.CompletePart, 0, len(request.Parts))
	for i, part := range request.Parts {
		if part.PartNumber != i+1 || strings.TrimSpace(part.ETag) == "" {
			h.writeError(w, r, ErrBadParts)
			return
		}
		parts = append(parts, minio.CompletePart{PartNumber: part.PartNumber, ETag: part.ETag})
	}
	// S3 must complete successfully before any durable job state is written.
	if err := h.store.CompleteMultipart(r.Context(), objectKey, multipartID, parts); err != nil {
		h.writeError(w, r, err)
		return
	}
	info, err := h.store.Stat(r.Context(), objectKey)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if info.Size <= 0 || (declaredSize > 0 && info.Size != declaredSize) {
		h.writeError(w, r, ErrSizeMismatch)
		return
	}
	transcodeJobID, contentID, err := h.repo.Complete(r.Context(), uploadJobID, principal.UserID, info.Size)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.notifier.Notify(r.Context(), transcodeJobID); err != nil {
		// A lost wakeup is harmless: the worker rescans PostgreSQL every idle round.
		h.log.Warn("transcode wakeup failed", "error", err, "job_id", transcodeJobID)
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"upload_job_id": uploadJobID, "content_id": contentID,
		"transcode_job_id": transcodeJobID, "status": "queued",
	})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	uploadJobID, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	result, err := h.repo.Status(r.Context(), uploadJobID, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, result)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_id", "invalid upload id",
			httpserver.RequestIDFromContext(r.Context()))
		return 0, false
	}
	return id, true
}

func randomKey() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	message := "unexpected server error"
	switch {
	case errors.Is(err, httpserver.ErrInvalidJSON), errors.Is(err, ErrInvalidInput),
		errors.Is(err, ErrTooLarge), errors.Is(err, ErrBadContentType),
		errors.Is(err, ErrBadParts), errors.Is(err, ErrSizeMismatch):
		status, code, message = http.StatusBadRequest, "invalid_input", err.Error()
	case errors.Is(err, ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "upload not found"
	}
	if status == http.StatusInternalServerError {
		h.log.Error("upload error", "error", err, "path", r.URL.Path)
	}
	httpserver.Error(w, status, code, message, httpserver.RequestIDFromContext(r.Context()))
}
