// Package content · 作者侧封面选择（UI 规范 §8.1「封面从视频帧中选择」）。
//
// 封面不是独立字段：feed / SEO / 落地页都取 playback 的 PosterURL，而后者来自
// artifact_manifests.manifest 的 poster/cover 文件条目。因此把用户选定的封面写成
// 一个版本化 poster 对象（media/{id}/v1/poster-{nano}.{ext}），再在同一个事务里
// 把 manifest 里的旧 poster 条目替换掉，全站封面就会自动跟随，且不会命中 CDN 上
// 旧的 poster.webp 缓存。图集以首图为封面，不走这条链路。
package content

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

var (
	ErrCoverType  = errors.New("图集以首图为封面，不需要单独选择封面")
	ErrNoManifest = errors.New("该作品还没有转码产物，暂时不能设置封面")
	ErrCoverState = errors.New("该作品状态不允许修改封面")
	ErrNoCover    = errors.New("该作品还没有封面")
)

// isPosterKey 与 playback.kindOf 的 poster 判定保持一致：
// 旧版固定 poster.webp，选帧后为 poster-{version}.{ext}。
func isPosterKey(key string) bool {
	base := path.Base(key)
	return base == "poster.webp" || strings.HasPrefix(base, "poster-")
}

func (h *Handler) registerCoverRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/contents/{id}/cover", h.guarded(h.setCover))
	mux.Handle("GET /api/v1/contents/{id}/cover", h.auth.RequireAuth(http.HandlerFunc(h.getCover)))
}

func (h *Handler) setCover(w http.ResponseWriter, r *http.Request) {
	if h.imageStore == nil {
		h.writeError(w, r, ErrStorageDown)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())

	// 与图集上传同口径：multipart 单图，魔数判定 MIME，8 MiB 上限。
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

	ctx := r.Context()
	tx, err := h.svc.repo.db.Begin(ctx)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var kind, state, slug string
	err = tx.QueryRow(ctx, `
		SELECT type, publish_state, slug FROM contents
		WHERE id = $1 AND author_id = $2 FOR UPDATE`, id, principal.UserID).
		Scan(&kind, &state, &slug)
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeError(w, r, ErrNotFound)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if kind != "video" {
		h.writeError(w, r, ErrCoverType)
		return
	}
	if state == "deleted" {
		h.writeError(w, r, ErrCoverState)
		return
	}

	var manifestID int64
	var payload []byte
	err = tx.QueryRow(ctx, `
		SELECT id, manifest::text FROM artifact_manifests
		WHERE content_id = $1 ORDER BY job_id DESC LIMIT 1 FOR UPDATE`, id).
		Scan(&manifestID, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeError(w, r, ErrNoManifest)
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var manifest map[string]any
	if err := json.Unmarshal(payload, &manifest); err != nil {
		h.writeError(w, r, fmt.Errorf("cover: parse manifest: %w", err))
		return
	}

	key := fmt.Sprintf("media/%d/v1/poster-%d%s", id, time.Now().UnixNano(), ext)
	if err := h.imageStore.PutBytes(ctx, key, data, mime,
		"public, max-age=31536000, immutable"); err != nil {
		h.writeError(w, r, fmt.Errorf("cover: write object: %w", err))
		return
	}
	// 下面任何一步失败都要清掉刚写的对象，避免留下无引用孤儿。
	rollbackObject := func() { _ = h.imageStore.Remove(ctx, key) }

	rawFiles, _ := manifest["files"].([]any)
	kept := make([]any, 0, len(rawFiles)+1)
	for _, entry := range rawFiles {
		fileMap, isMap := entry.(map[string]any)
		if !isMap {
			kept = append(kept, entry)
			continue
		}
		entryKey, _ := fileMap["key"].(string)
		if isPosterKey(entryKey) {
			continue // 旧封面让位给新版本；cover-* 条目保留作历史与回退
		}
		kept = append(kept, entry)
	}
	kept = append(kept, map[string]any{
		"key": key, "byte_size": len(data), "content_type": mime,
	})
	manifest["files"] = kept
	updated, err := json.Marshal(manifest)
	if err != nil {
		rollbackObject()
		h.writeError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE artifact_manifests SET manifest = $2::jsonb WHERE id = $1`,
		manifestID, updated); err != nil {
		rollbackObject()
		h.writeError(w, r, err)
		return
	}
	if state == "published" {
		// 已发布内容换封面要让预渲染页 / CDN 失效（§7.5）。
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			VALUES ($1, 'content.updated',
			  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text))
			ON CONFLICT (event_key) DO NOTHING`,
			fmt.Sprintf("content.updated:%d:%d", id, time.Now().UnixNano()),
			id, slug); err != nil {
			rollbackObject()
			h.writeError(w, r, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		rollbackObject()
		h.writeError(w, r, err)
		return
	}
	url, err := h.imageStore.MediaURL(ctx, key)
	if err != nil {
		h.writeError(w, r, fmt.Errorf("cover: sign url: %w", err))
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"key": key, "url": url})
}

// GET /api/v1/contents/{id}/cover —— 当前封面（供发布页 / 编辑页回填）。
func (h *Handler) getCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	ctx := r.Context()
	var owner int64
	err := h.svc.repo.db.QueryRow(ctx, `
		SELECT author_id FROM contents WHERE id = $1 AND publish_state <> 'deleted'`, id).
		Scan(&owner)
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
	var payload []byte
	err = h.svc.repo.db.QueryRow(ctx, `
		SELECT manifest::text FROM artifact_manifests
		WHERE content_id = $1 ORDER BY job_id DESC LIMIT 1`, id).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		httpserver.JSON(w, http.StatusOK, map[string]any{"key": "", "url": ""})
		return
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var manifest struct {
		Files []struct {
			Key string `json:"key"`
		} `json:"files"`
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		h.writeError(w, r, err)
		return
	}
	var key string
	for _, file := range manifest.Files {
		if isPosterKey(file.Key) {
			key = file.Key // 版本化条目只有一条；取最后一条以兼容历史数据
		}
	}
	if key == "" {
		httpserver.JSON(w, http.StatusOK, map[string]any{"key": "", "url": ""})
		return
	}
	url := key
	if h.imageStore != nil {
		signed, err := h.imageStore.MediaURL(ctx, key)
		if err == nil {
			url = signed
		}
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"key": key, "url": url})
}
