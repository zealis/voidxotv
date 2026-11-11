// Package content · 作者侧图集能力（UI 规范 §8.1「图集发布」/ 技术规范 §3.2「2–18 张」）。
//
// 图集以「一个 type=image 的 content + 多条有序 media_assets(kind=image)」表达。
// feed 侧此前已有完整的消费链路（GalleryStrip / /image/[slug] / 图片 sitemap），
// 但生产代码里没有任何写入路径——图片全部来自 scripts/seed-demo.sql。本文件补齐作者侧：
// 上传单图（服务端直写对象存储）、列表、整批重排、删除单张，并在发布时校验张数。
//
// 图片走小文件直传，不复用视频的分片上传 + ffmpeg 转码管线：
// 单图限制 8 MiB，PutBytes 一次写入，事务里同时登记 media_assets 与 storage_objects。
// storage_objects.driver 固定 's3'，feed 的 images() 会用 PublicURL(key) 还原可访问地址。
package content

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

const (
	maxGalleryImages = 18
	minGalleryImages = 2
	maxImageBytes    = 8 << 20 // 单张 8 MiB
)

// 允许的 MIME 由魔数探测（http.DetectContentType）决定，不信任客户端声明。
var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

var (
	ErrGallerySize  = fmt.Errorf("图集需要 %d–%d 张图片", minGalleryImages, maxGalleryImages)
	ErrGalleryLimit = fmt.Errorf("图集最多 %d 张图片", maxGalleryImages)
	ErrGalleryType  = errors.New("该作品不是图集草稿，不能管理图片")
	ErrBadImage     = errors.New("图片格式不支持、内容不是图片或超过 8 MiB")
	ErrGalleryOrder = errors.New("排序列表必须与当前图集图片完全一致")
	ErrGalleryAsset = errors.New("图片不存在")
	ErrStorageDown  = errors.New("对象存储暂不可用，无法上传图片")
)

// GalleryItem 是发布页 / 草稿恢复拿到的统一形状。
type GalleryItem struct {
	ID       int64  `json:"id"`
	Position int    `json:"position"`
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	ByteSize int64  `json:"byte_size"`
	Width    *int   `json:"width,omitempty"`
	Height   *int   `json:"height,omitempty"`
}

// galleryQuerier 让列表函数在连接池与事务里都能查。
type galleryQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// SetImageStore 由 cmd/api 在对象存储可用时注入。S3 是可选启动的，
// 未注入时图集上传返回明确的 503，不影响其余接口。
func (h *Handler) SetImageStore(store *objectstore.Store, bucket string) {
	h.imageStore, h.imageBucket = store, bucket
}

func (h *Handler) registerGalleryRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/contents/{id}/images", h.guarded(h.uploadImage))
	mux.Handle("GET /api/v1/contents/{id}/images", h.auth.RequireAuth(http.HandlerFunc(h.listImages)))
	mux.Handle("PUT /api/v1/contents/{id}/images/order", h.guarded(h.reorderImages))
	mux.Handle("DELETE /api/v1/contents/{id}/images/{assetID}", h.guarded(h.removeImage))
}

// lockGallery 在事务里锁住内容行，确认是本人、且确实是图集。
// allowPublished 控制是否允许已发布/定时作品：排序（封面）与增删图片都需要它，
// 已发布作品的图片变更必须写 content.updated 触发页面再验证（§7.5）。
// 行锁把并发上传 / 排序串行化，避免 position 撞号。
func lockGallery(ctx context.Context, tx pgx.Tx, contentID, userID int64, allowPublished bool) (state, slug string, err error) {
	var kind string
	err = tx.QueryRow(ctx, `
		SELECT type, publish_state, slug FROM contents
		WHERE id = $1 AND author_id = $2 FOR UPDATE`, contentID, userID).Scan(&kind, &state, &slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if kind != "image" {
		return "", "", ErrGalleryType
	}
	if state == "draft" {
		return state, slug, nil
	}
	if allowPublished && (state == "published" || state == "scheduled") {
		return state, slug, nil
	}
	return "", "", ErrNotDraft
}

func (h *Handler) galleryItems(ctx context.Context, q galleryQuerier, contentID int64) ([]GalleryItem, error) {
	rows, err := q.Query(ctx, `
		SELECT m.id, m.position, m.mime_type, m.byte_size, m.width, m.height,
		       COALESCE(s.driver, ''), COALESCE(s.object_key, '')
		FROM media_assets m
		LEFT JOIN storage_objects s ON s.media_asset_id = m.id
		WHERE m.content_id = $1 AND m.kind = 'image'
		ORDER BY m.position, m.id`, contentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]GalleryItem, 0)
	for rows.Next() {
		var item GalleryItem
		var driver, key string
		if err := rows.Scan(&item.ID, &item.Position, &item.MimeType, &item.ByteSize,
			&item.Width, &item.Height, &driver, &key); err != nil {
			return nil, err
		}
		item.URL = key
		if driver == "s3" && h.imageStore != nil {
			url, err := h.imageStore.MediaURL(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("gallery url: %w", err)
			}
			item.URL = url
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func galleryKey(userID, contentID int64, ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("images/%d/%d/%s%s", userID, contentID,
		base64.RawURLEncoding.EncodeToString(buf), ext), nil
}

func respondItems(w http.ResponseWriter, items []GalleryItem) {
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// POST /api/v1/contents/{id}/images —— multipart/form-data，字段名 image。
func (h *Handler) uploadImage(w http.ResponseWriter, r *http.Request) {
	if h.imageStore == nil {
		h.writeError(w, r, ErrStorageDown)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())

	// MaxBytesReader 兜住超大 body；ParseMultipartForm 的内存阈值也设为单图上限。
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		h.writeError(w, r, ErrBadImage)
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		h.writeError(w, r, ErrBadImage)
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size <= 0 || header.Size > maxImageBytes {
		h.writeError(w, r, ErrBadImage)
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		h.writeError(w, r, ErrBadImage)
		return
	}
	probe := data
	if len(probe) > 512 {
		probe = probe[:512]
	}
	mime := http.DetectContentType(probe)
	ext, allowed := allowedImageTypes[mime]
	if !allowed {
		h.writeError(w, r, ErrBadImage)
		return
	}
	// 尽力记录原始尺寸；webp 等标准库未注册的解码器会失败，留 NULL 即可。
	var width, height *int
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		w, hh := cfg.Width, cfg.Height
		width, height = &w, &hh
	}

	key, err := galleryKey(principal.UserID, id, ext)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	ctx := r.Context()
	if err := h.imageStore.PutBytes(ctx, key, data, mime,
		"public, max-age=31536000, immutable"); err != nil {
		h.log.Error("gallery put failed", "error", err, "key", key)
		h.writeError(w, r, fmt.Errorf("write image object: %w", err))
		return
	}

	tx, err := h.svc.repo.db.Begin(ctx)
	if err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, slug, err := lockGallery(ctx, tx, id, principal.UserID, true)
	if err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	var count, position int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), COALESCE(MAX(position), -1) + 1
		FROM media_assets WHERE content_id = $1 AND kind = 'image'`, id).
		Scan(&count, &position); err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	if count >= maxGalleryImages {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, ErrGalleryLimit)
		return
	}
	var assetID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO media_assets
		    (content_id, kind, position, mime_type, byte_size, width, height, processing_status)
		VALUES ($1, 'image', $2, $3, $4, $5, $6, 'ready') RETURNING id`,
		id, position, mime, len(data), width, height).Scan(&assetID); err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO storage_objects (media_asset_id, driver, bucket, object_key, byte_size)
		VALUES ($1, 's3', $2, $3, $4)`, assetID, h.imageBucket, key, len(data)); err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	items, err := h.galleryItems(ctx, tx, id)
	if err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		_ = h.imageStore.Remove(ctx, key)
		h.writeError(w, r, err)
		return
	}
	// 已发布图集新增图片：页面内容变了，触发预渲染页再验证（与排序同口径，§7.5）。
	if state == "published" {
		if _, err := h.svc.repo.db.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			VALUES ($1, 'content.updated',
			  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text))
			ON CONFLICT (event_key) DO NOTHING`,
			fmt.Sprintf("content.updated:%d:%d", id, time.Now().UnixNano()), id, slug); err != nil {
			h.log.Warn("gallery upload: content.updated failed", "error", err)
		}
	}
	var created GalleryItem
	for _, item := range items {
		if item.ID == assetID {
			created = item
		}
	}
	httpserver.JSON(w, http.StatusCreated, map[string]any{"item": created, "items": items})
}

// GET /api/v1/contents/{id}/images —— 草稿恢复 / 编辑器回填。
func (h *Handler) listImages(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	ctx := r.Context()
	var owner int64
	err := h.svc.repo.db.QueryRow(ctx, `
		SELECT author_id FROM contents WHERE id = $1 AND publish_state <> 'deleted'`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeError(w, r, ErrNotFound)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if owner != principal.UserID {
		h.writeError(w, r, ErrForbidden)
		return
	}
	items, err := h.galleryItems(ctx, h.svc.repo.db, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	respondItems(w, items)
}

type reorderRequest struct {
	IDs []int64 `json:"ids"`
}

// PUT /api/v1/contents/{id}/images/order —— 前端拖拽后传全量顺序，首图 = ids[0]。
func (h *Handler) reorderImages(w http.ResponseWriter, r *http.Request) {
	var request reorderRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if len(request.IDs) == 0 || len(request.IDs) > maxGalleryImages {
		h.writeError(w, r, ErrGalleryOrder)
		return
	}
	seen := make(map[int64]bool, len(request.IDs))
	for _, assetID := range request.IDs {
		if seen[assetID] {
			h.writeError(w, r, ErrGalleryOrder)
			return
		}
		seen[assetID] = true
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	ctx := r.Context()
	tx, err := h.svc.repo.db.Begin(ctx)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, slug, err := lockGallery(ctx, tx, id, principal.UserID, true)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM media_assets WHERE content_id = $1 AND kind = 'image'`, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var current []int64
	for rows.Next() {
		var assetID int64
		if err := rows.Scan(&assetID); err != nil {
			rows.Close()
			h.writeError(w, r, err)
			return
		}
		current = append(current, assetID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		h.writeError(w, r, err)
		return
	}
	rows.Close()
	if len(current) != len(request.IDs) {
		h.writeError(w, r, ErrGalleryOrder)
		return
	}
	for _, assetID := range current {
		if !seen[assetID] {
			h.writeError(w, r, ErrGalleryOrder)
			return
		}
	}
	for position, assetID := range request.IDs {
		if _, err := tx.Exec(ctx, `
			UPDATE media_assets SET position = $3
			WHERE id = $1 AND content_id = $2 AND kind = 'image'`, assetID, id, position); err != nil {
			h.writeError(w, r, err)
			return
		}
	}
	items, err := h.galleryItems(ctx, tx, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// 已发布作品的首图就是封面：顺序变了等于封面变了，必须触发页面再验证（§7.5）。
	// 定时作品还没有公开页面，不需要事件。
	if state == "published" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			VALUES ($1, 'content.updated',
			  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text))
			ON CONFLICT (event_key) DO NOTHING`,
			fmt.Sprintf("content.updated:%d:%d", id, time.Now().UnixNano()), id, slug); err != nil {
			h.writeError(w, r, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.writeError(w, r, err)
		return
	}
	respondItems(w, items)
}

// DELETE /api/v1/contents/{id}/images/{assetID} —— 删单张并把剩余 position 压成连续。
func (h *Handler) removeImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	assetID, err := strconv.ParseInt(r.PathValue("assetID"), 10, 64)
	if err != nil || assetID <= 0 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_id", "invalid image id",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	ctx := r.Context()
	tx, err := h.svc.repo.db.Begin(ctx)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, slug, err := lockGallery(ctx, tx, id, principal.UserID, true)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var driver, key string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(s.driver, ''), COALESCE(s.object_key, '')
		FROM media_assets m LEFT JOIN storage_objects s ON s.media_asset_id = m.id
		WHERE m.id = $1 AND m.content_id = $2 AND m.kind = 'image'`, assetID, id).
		Scan(&driver, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeError(w, r, ErrGalleryAsset)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM media_assets WHERE content_id = $1 AND kind = 'image'`, id).
		Scan(&count); err != nil {
		h.writeError(w, r, err)
		return
	}
	// 已发布 / 定时图集至少保留 2 张（§3.2：2–18 张）；草稿在发布时校验，允许临时 1 张。
	if state != "draft" && count <= minGalleryImages {
		h.writeError(w, r, fmt.Errorf("%w：已发布图集至少保留 %d 张", ErrGallerySize, minGalleryImages))
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storage_objects WHERE media_asset_id = $1`, assetID); err != nil {
		h.writeError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, assetID); err != nil {
		h.writeError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_assets m SET position = ordered.pos
		FROM (SELECT id, row_number() OVER (ORDER BY position, id) - 1 AS pos
		      FROM media_assets WHERE content_id = $1 AND kind = 'image') AS ordered
		WHERE m.id = ordered.id`, id); err != nil {
		h.writeError(w, r, err)
		return
	}
	items, err := h.galleryItems(ctx, tx, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.writeError(w, r, err)
		return
	}
	// 已发布图集删图：内容与封面都可能变化，触发页面再验证（§7.5）。
	if state == "published" {
		if _, err := h.svc.repo.db.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			VALUES ($1, 'content.updated',
			  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text))
			ON CONFLICT (event_key) DO NOTHING`,
			fmt.Sprintf("content.updated:%d:%d", id, time.Now().UnixNano()), id, slug); err != nil {
			h.log.Warn("gallery remove: content.updated failed", "error", err)
		}
	}
	// DB 已提交，对象存储清理失败不回滚业务结果（孤儿对象由生命周期规则回收）。
	if driver == "s3" && key != "" && h.imageStore != nil {
		if err := h.imageStore.Remove(ctx, key); err != nil {
			h.log.Warn("gallery object cleanup failed", "error", err, "key", key)
		}
	}
	respondItems(w, items)
}

// GalleryStats 供发布前校验图集张数（§3.2 沉浸流准入）。
func (r *Repository) GalleryStats(ctx context.Context, contentID, authorID int64) (string, int, error) {
	var kind string
	err := r.db.QueryRow(ctx, `
		SELECT type FROM contents
		WHERE id = $1 AND author_id = $2 AND publish_state <> 'deleted'`, contentID, authorID).
		Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	var count int
	if err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM media_assets
		WHERE content_id = $1 AND kind = 'image' AND processing_status = 'ready'`, contentID).
		Scan(&count); err != nil {
		return "", 0, err
	}
	return kind, count, nil
}
