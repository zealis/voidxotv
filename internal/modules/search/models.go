// Package search implements the unified database-backed retrieval path from
// 开发技术规范 §9.2: PostgreSQL ILIKE substring matching behind a single
// interface, so switching to tsvector or an external engine later only changes
// this package and never the business layer.
package search

import (
	"time"

	"github.com/yejie/voidxotv/internal/modules/feed"
)

// Criteria is the normalised, already-validated search input.
type Criteria struct {
	// Query is the trimmed user input (original casing kept for display).
	Query string
	// Normalized is the lowercase, whitespace-collapsed form used for stats.
	Normalized string
	// Type filters video/image; empty means both.
	Type string
	// CategoryID filters by category; 0 means all categories.
	CategoryID int64
	// Duration bucket: 0 全部, 1 ≤60s, 2 60–180s, 3 >180s. Only videos carry
	// durations, so a non-zero bucket narrows results to video automatically.
	Duration int
	// ViewerID enables "my own content" visibility; 0 means anonymous.
	ViewerID int64
}

// Response is the public search envelope.
type Response struct {
	Query      string      `json:"query"`
	Items      []feed.Item `json:"items"`
	NextCursor any         `json:"next_cursor"`
	Count      int         `json:"count"`
	TookMS     int64       `json:"took_ms"`
	// Filters echoes the applied filters so the client never has to guess.
	Filters AppliedFilters `json:"filters"`
}

type AppliedFilters struct {
	Type       string   `json:"type,omitempty"`
	Category   string   `json:"category,omitempty"`
	Duration   int      `json:"duration"`
	DurationMS [2]int64 `json:"duration_range_ms"`
}

// Category is a public category with its public content count.
type Category struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Position     int    `json:"position"`
	ContentCount int64  `json:"content_count"`
}

// HotQuery is one aggregated search term.
type HotQuery struct {
	Query string `json:"query"`
	Hits  int64  `json:"hits"`
}

// ItemTags carries the tags rendered under a search result.
type ItemTags struct {
	ContentID int64    `json:"content_id"`
	Tags      []string `json:"tags"`
}

// QueryRecord is the audit/stats row written for every executed search.
type QueryRecord struct {
	UserID      int64
	Query       string
	Normalized  string
	ResultCount int
	Duration    time.Duration
	Filters     map[string]any
}
