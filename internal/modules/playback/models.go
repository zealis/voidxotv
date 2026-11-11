// Package playback issues short-lived media URLs without proxying media bytes.
// Public content is served from the CDN-style public endpoint; private, friends-only
// and author-preview content receives 30-minute SigV4 URLs (开发技术规范 §6 / §11).
package playback

import "time"

type File struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	URL         string `json:"url"`
}

type Playback struct {
	Status           string            `json:"status"`
	ContentID        int64             `json:"content_id"`
	Slug             string            `json:"slug"`
	Mode             string            `json:"mode"`
	DurationMS       int64             `json:"duration_ms"`
	Renditions       []string          `json:"renditions"`
	Signed           bool              `json:"signed"`
	ExpiresAt        *time.Time        `json:"expires_at,omitempty"`
	ProfileVersion   string            `json:"profile_version,omitempty"`
	Files            []File            `json:"files"`
	MediaURL         string            `json:"media_url,omitempty"`
	PosterURL        string            `json:"poster_url,omitempty"`
	Playlists        map[string]string `json:"playlists,omitempty"`
	Warnings         []string          `json:"warnings,omitempty"`
	MasterPlaylist   string            `json:"master_playlist,omitempty"`
	PlaylistBasePath string            `json:"playlist_base_path,omitempty"`
}
