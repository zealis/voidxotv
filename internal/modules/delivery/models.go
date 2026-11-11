// Package delivery is the Go 后端内的交付调度模块（开发技术规范 §10）。
// 它不渲染、不持有页面产物，只消费 Outbox 事件与 delivery_jobs，
// 调度 Nuxt ISR 再验证、维护版本标记并触发 CDN 精确失效。
package delivery

import "time"

type OutboxEvent struct {
	ID      int64
	Key     string
	Type    string
	Payload []byte
}

type ContentRef struct {
	ID     int64  `json:"content_id"`
	Slug   string `json:"slug"`
	Type   string `json:"type"`
	Handle string `json:"handle"`
	// Categories 是该内容所属分类的 slug，用于关联失效 /category/**（§10.3）。
	Categories []string `json:"categories,omitempty"`
	// PreviousCategories 来自分类变更事件：旧分类页同样需要失效。
	PreviousCategories []string `json:"prev_categories,omitempty"`
}

type Job struct {
	ID            int64
	PageKey       string
	PageType      string
	TriggerType   string
	TargetVersion int64
	AttemptCount  int
	MaxAttempts   int
}

type Page struct {
	PageKey          string     `json:"page_key"`
	PageType         string     `json:"page_type"`
	DesiredVersion   int64      `json:"desired_version"`
	PublishedVersion int64      `json:"published_version"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	LastRevalidated  *time.Time `json:"last_revalidated_at,omitempty"`
	Status           string     `json:"status"`
}

type JobView struct {
	ID            int64      `json:"id"`
	PageKey       string     `json:"page_key"`
	PageType      string     `json:"page_type"`
	TriggerType   string     `json:"trigger_type"`
	Status        string     `json:"status"`
	TargetVersion int64      `json:"target_version"`
	Attempts      int        `json:"attempt_count"`
	LastError     string     `json:"last_error,omitempty"`
	ScheduledAt   *time.Time `json:"scheduled_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type RevalidateResult struct {
	Requested []string `json:"requested"`
	Removed   []string `json:"removed"`
	Warmed    []string `json:"warmed"`
}

type scheduledPage struct {
	Key   string
	Kind  string
	Delay time.Duration
}
