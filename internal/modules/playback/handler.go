package playback

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/content"
	"github.com/yejie/voidxotv/internal/modules/transcode"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

type Handler struct {
	repo       *Repository
	store      *objectstore.Store
	content    *content.Service
	auth       *auth.Handler
	publicRead bool
	log        *slog.Logger
}

func NewHandler(repo *Repository, store *objectstore.Store, contentService *content.Service,
	authHandler *auth.Handler, publicRead bool, log *slog.Logger) *Handler {
	return &Handler{
		repo: repo, store: store, content: contentService, auth: authHandler,
		publicRead: publicRead, log: log,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Public deep-link playback obeys publish/review/visibility rules (404 otherwise).
	mux.HandleFunc("GET /api/v1/c/{slug}/playback", h.publicPlayback)
	// Authors may preview their own not-yet-approved content; always signed.
	mux.Handle("GET /api/v1/contents/{id}/playback", h.auth.RequireAuth(http.HandlerFunc(h.ownerPlayback)))
}

func (h *Handler) publicPlayback(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		h.notFound(w, r)
		return
	}
	item, err := h.content.GetBySlug(r.Context(), slug)
	if err != nil {
		h.notFound(w, r)
		return
	}
	viewerID := int64(0)
	if principal, ok := h.auth.OptionalPrincipal(r); ok {
		viewerID = principal.UserID
	}
	visible, err := h.content.Visible(r.Context(), item, viewerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !visible {
		h.notFound(w, r)
		return
	}
	// Public content only uses unsigned CDN URLs when the bucket/CDN really
	// allows anonymous reads; private buckets still need short-lived signatures.
	h.respond(w, r, item, !h.publicRead || item.Visibility != "public")
}

func (h *Handler) ownerPlayback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpserver.Error(w, http.StatusBadRequest, "invalid_id", "invalid content id",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	item, err := h.content.Owned(r.Context(), id, principal.UserID)
	if err != nil {
		h.notFound(w, r)
		return
	}
	h.respond(w, r, item, true)
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, item content.Content, signed bool) {
	result, err := h.Build(r.Context(), item, signed)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpserver.JSON(w, http.StatusOK, result)
}

// Build returns the full playback descriptor, including rewritten HLS playlists.
func (h *Handler) Build(ctx context.Context, item content.Content, signed bool) (Playback, error) {
	return h.build(ctx, item, signed, true)
}

// Summary signs only the poster and the primary media. Feed lists use it so that
// off-screen HLS items never cause dozens of segment signatures or playlist reads.
func (h *Handler) Summary(ctx context.Context, item content.Content, signed bool) (Playback, error) {
	return h.build(ctx, item, signed, false)
}

func (h *Handler) build(ctx context.Context, item content.Content, signed, full bool) (Playback, error) {
	result := Playback{
		Status: "processing", ContentID: item.ID, Slug: item.Slug, Mode: "none",
		Signed: signed, Files: []File{},
	}
	payload, err := h.repo.LatestManifest(ctx, item.ID)
	if errors.Is(err, ErrNoManifest) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var manifest transcode.Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return result, err
	}

	expires := time.Now().UTC().Add(objectstore.PlaybackTTL)
	if signed {
		result.ExpiresAt = &expires
	}
	urls := make(map[string]string, len(manifest.Files))
	for _, file := range manifest.Files {
		kind := kindOf(file.Key)
		if !full && !summaryKind(kind) {
			continue
		}
		address := h.store.PublicURL(file.Key)
		if signed {
			address, err = h.store.PresignGet(ctx, file.Key, objectstore.PlaybackTTL)
			if err != nil {
				return result, err
			}
		}
		urls[file.Key] = address
		result.Files = append(result.Files, File{
			Key: file.Key, Kind: kind, ContentType: file.ContentType,
			ByteSize: file.ByteSize, URL: address,
		})
	}
	result.DurationMS = manifest.DurationMS
	result.Renditions = manifest.Renditions
	result.ProfileVersion = manifest.ProfileVersion
	result.Warnings = manifest.Warnings

	prefix := "media/" + strconv.FormatInt(item.ID, 10) + "/v1/"
	result.PlaylistBasePath = prefix
	result.Playlists = map[string]string{}
	if full {
		for _, file := range result.Files {
			switch file.Kind {
			case "master", "playlist":
				text, err := h.rewritePlaylist(ctx, file.Key, urls)
				if err != nil {
					// A missing playlist is not fatal: the MP4 fallback is still playable.
					h.log.Warn("playback: rewrite playlist", "key", file.Key, "error", err)
					continue
				}
				relative := strings.TrimPrefix(file.Key, prefix)
				result.Playlists[relative] = text
				if file.Kind == "master" {
					result.MasterPlaylist = text
				}
			}
		}
	}

	for _, file := range result.Files {
		switch file.Kind {
		case "master", "mp4":
			mode := "mp4"
			if file.Kind == "master" {
				mode = "hls"
			}
			if result.Mode == "none" || (result.Mode == "mp4" && mode == "hls") {
				result.Mode, result.MediaURL = mode, file.URL
			}
		case "poster", "cover":
			if result.PosterURL == "" || file.Kind == "poster" {
				result.PosterURL = file.URL
			}
		}
	}
	if result.Mode != "none" {
		result.Status = "ready"
	}
	return result, nil
}

var uriAttribute = regexp.MustCompile(`URI="([^"]+)"`)

// rewritePlaylist resolves relative HLS references to absolute (possibly signed) URLs.
// Only playlist text is read by the app; the media segments themselves stay on the CDN.
func (h *Handler) rewritePlaylist(ctx context.Context, key string, urls map[string]string) (string, error) {
	data, err := h.store.GetBytes(ctx, key, 1<<20)
	if err != nil {
		return "", err
	}
	return rewritePlaylistText(data, key, urls), nil
}

func rewritePlaylistText(data []byte, key string, urls map[string]string) string {
	directory := path.Dir(key)
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lines[i] = uriAttribute.ReplaceAllStringFunc(line, func(match string) string {
				inner := match[len(`URI="`) : len(match)-1]
				if resolved := resolveURI(directory, inner, urls); resolved != "" {
					return `URI="` + resolved + `"`
				}
				return match
			})
			continue
		}
		if resolved := resolveURI(directory, trimmed, urls); resolved != "" {
			lines[i] = resolved
		}
	}
	return strings.Join(lines, "\n")
}

func resolveURI(directory, uri string, urls map[string]string) string {
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") ||
		strings.HasPrefix(uri, "data:") {
		return ""
	}
	uri = strings.SplitN(uri, "?", 2)[0]
	if uri == "" {
		return ""
	}
	if address, ok := urls[path.Clean(path.Join(directory, uri))]; ok {
		return address
	}
	// Older manifests referenced "480p/index.m3u8" while the artifact lives at
	// "hls/480p/index.m3u8"; keep those already-published playlists playable.
	if address, ok := urls[path.Clean(path.Join(directory, "hls", uri))]; ok {
		return address
	}
	return ""
}

func kindOf(key string) string {
	base := path.Base(key)
	switch {
	case base == "master.m3u8":
		return "master"
	case strings.HasSuffix(base, ".m3u8"):
		return "playlist"
	case base == "init.mp4":
		return "init"
	case strings.HasSuffix(base, ".m4s"):
		return "segment"
	case base == "poster.webp":
		return "poster"
	// 作者选帧后写的是版本化 poster-{version}.{ext}，仍按封面处理（§8.1）。
	case strings.HasPrefix(base, "poster-"):
		return "poster"
	case strings.HasPrefix(base, "cover-"):
		return "cover"
	case base == "storyboard.webp":
		return "storyboard"
	case base == "preview.vtt":
		return "preview"
	case base == "mezzanine.mp4":
		return "mezzanine"
	// 可选 HEVC 流与 H.264 主档分开标记，避免被当作默认播放源。
	case strings.HasSuffix(base, "-hevc.mp4"):
		return "mp4_hevc"
	case strings.HasSuffix(base, ".mp4"):
		return "mp4"
	default:
		return "other"
	}
}

func summaryKind(kind string) bool {
	switch kind {
	// mp4_hevc 一并下发：列表虽然不播它，但客户端需要判断能否在自动播放时选流。
	case "master", "mp4", "mp4_hevc", "poster", "cover":
		return true
	default:
		return false
	}
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	httpserver.Error(w, http.StatusNotFound, "not_found", "content not found",
		httpserver.RequestIDFromContext(r.Context()))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("playback error", "error", err, "path", r.URL.Path)
	httpserver.Error(w, http.StatusInternalServerError, "internal_error", "unexpected server error",
		httpserver.RequestIDFromContext(r.Context()))
}
