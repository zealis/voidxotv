// Package feed serves the public immersive stream. Only content that is
// published, approved, public and feed-eligible can ever appear here
// (开发技术规范 §3.3 部分索引条件).
package feed

import (
	"errors"
	"time"
)

// ErrNoPlayableSource marks published content whose media never produced a
// playable artifact. Listings skip it and deep links treat it as not found,
// because 沉浸流资格 requires media availability (§3.2).
var ErrNoPlayableSource = errors.New("feed: no playable media source")

type Author struct {
	ID        int64  `json:"id"`
	Handle    string `json:"handle"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// Stats 是 compose 实时查出的真实互动计数（reactions / comments，§6.2 角标口径）。
// shares 暂无数据源，恒为 0。
type Stats struct {
	Likes     int64 `json:"likes"`
	Comments  int64 `json:"comments"`
	Favorites int64 `json:"favorites"`
	Shares    int64 `json:"shares"`
}

type Media struct {
	Mode           string            `json:"mode"`
	PosterURL      string            `json:"poster_url,omitempty"`
	MediaURL       string            `json:"media_url,omitempty"`
	DurationMS     int64             `json:"duration_ms"`
	Signed         bool              `json:"signed"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
	MasterPlaylist string            `json:"master_playlist,omitempty"`
	Playlists      map[string]string `json:"playlists,omitempty"`
	Status         string            `json:"status"`
	// HEVCURL 是可选的 HEVC 源（§5.1）。客户端按 canPlayType 探测后再切换，
	// H.264 的 MediaURL 始终是默认可播回退。
	HEVCURL string `json:"hevc_url,omitempty"`
}

// POI 是发布时添加的位置信息（§8 发布设置）：名称必填、地址可选。
type POI struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

type Item struct {
	ID          int64      `json:"id"`
	Slug        string     `json:"slug"`
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Author      Author     `json:"author"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Stats       Stats      `json:"stats"`
	Media       Media      `json:"media"`
	Images      []string   `json:"images,omitempty"`
	// Tags 仅搜索结果与分类列表填充，用于结果卡的语义标签。
	Tags []string `json:"tags,omitempty"`
	// POI 仅详情下发（full=true），列表条目不查以省一次 join。
	POI *POI `json:"poi,omitempty"`
	// Liked / Favorited 是当前登录查看者自己的点赞 / 收藏标记（§6.2 操作栏回显）。
	// 仅 feed 列表与详情下发；匿名响应与公开缓存页（creator / topic）省略。
	Liked     bool `json:"liked,omitempty"`
	Favorited bool `json:"favorited,omitempty"`
}

type Response struct {
	Items []Item `json:"items"`
	// NextCursor 是不透明游标：规则排序为 "score:id"，按时间排序为整数 id，
	// 末页为 null。客户端必须原样回传，不得解析。
	NextCursor any `json:"next_cursor"`
	Count      int `json:"count"`
}
