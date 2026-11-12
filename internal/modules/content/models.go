// Package content owns drafts, publishing and visibility of the unified content model.
package content

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("content not found")
	ErrForbidden    = errors.New("not allowed to modify this content")
	ErrInvalidInput = errors.New("invalid content input")
	ErrInvalidType  = errors.New("content type must be video or image")
	ErrTitleTooLong = errors.New("title exceeds 30 characters")
	ErrBodyTooLong  = errors.New("description exceeds 200 characters")
	ErrVisibility   = errors.New("invalid visibility")
	ErrScheduleSoon = errors.New("scheduled time must be at least 10 minutes ahead")
	ErrNotDraft     = errors.New("only drafts can be edited")
)

// Content mirrors the contents table; POI stays as raw JSONB.
type Content struct {
	ID           int64           `json:"id"`
	Slug         string          `json:"slug"`
	AuthorID     int64           `json:"author_id"`
	Type         string          `json:"type"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Visibility   string          `json:"visibility"`
	PublishState string          `json:"publish_state"`
	ReviewStatus string          `json:"review_status"`
	ScheduledAt  *time.Time      `json:"scheduled_at,omitempty"`
	PublishedAt  *time.Time      `json:"published_at,omitempty"`
	POI          json.RawMessage `json:"poi,omitempty"`
	FeedEligible bool            `json:"feed_eligible"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	// MediaStatus 不是 contents 列：仅「我的作品」列表为视频补充转码状态
	// （ready / processing / failed / none），前端据此显示「处理中」徽标（§10）。
	MediaStatus string `json:"media_status,omitempty"`
	// 卡片网格字段：仅「我的作品 / 喜欢 / 收藏」列表批量补齐，其它接口不带。
	PosterURL string `json:"poster_url,omitempty"`
	LikeCount int64  `json:"like_count,omitempty"`
}

type CreateDraftInput struct {
	Type        string
	Title       string
	Description string
}

type UpdateDraftInput struct {
	Title       string
	Description string
	Visibility  string
	POI         json.RawMessage
	// 定时设置随草稿一起保存，刷新发布页可恢复；不改变 publish_state。
	ScheduledAt *time.Time
}

type PublishInput struct {
	Visibility  string
	ScheduledAt *time.Time
	POI         json.RawMessage
}

const (
	maxTitleRunes       = 30
	maxDescriptionRunes = 200
	minScheduleLead     = 10 * time.Minute
)
