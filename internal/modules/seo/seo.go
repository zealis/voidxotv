// Package seo is the single source of public metadata for Nuxt head, JSON-LD
// and sitemaps (开发技术规范 §11.2：字段同源，不得前后端各拼一套).
// Only published + approved + public + feed-eligible content is exposed.
package seo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

var ErrNotFound = errors.New("seo: content not indexable")

type Page struct {
	ID           int64    `json:"id"`
	Slug         string   `json:"slug"`
	Type         string   `json:"type"`
	Path         string   `json:"path"`
	Canonical    string   `json:"canonical"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Handle       string   `json:"handle"`
	Creator      string   `json:"creator"`
	PublishedAt  string   `json:"published_at"`
	ThumbnailURL string   `json:"thumbnail_url,omitempty"`
	ContentURL   string   `json:"content_url,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
	DurationISO  string   `json:"duration_iso,omitempty"`
	Images       []string `json:"images,omitempty"`
	Indexable    bool     `json:"indexable"`
}

type Repository struct {
	db      *pgxpool.Pool
	store   *objectstore.Store
	siteURL string
}

func NewRepository(db *pgxpool.Pool, store *objectstore.Store, siteURL string) *Repository {
	return &Repository{db: db, store: store, siteURL: strings.TrimRight(siteURL, "/")}
}

type rawRow struct {
	id           int64
	slug         string
	kind         string
	title        string
	description  string
	handle       string
	publishedAt  time.Time
	durationMS   int64
	manifestText string
}

const selectColumns = `
	c.id, c.slug, c.type, c.title, c.description, u.handle,
	COALESCE(c.published_at, 'epoch'::timestamptz), COALESCE(c.duration_ms, 0),
	COALESCE((SELECT m.manifest::text FROM artifact_manifests m
	          WHERE m.content_id = c.id ORDER BY m.job_id DESC LIMIT 1), '')`

const publicFilter = `
	c.publish_state = 'published' AND c.review_status = 'approved'
	AND c.visibility = 'public' AND c.feed_eligible`

func (r *Repository) List(ctx context.Context, cursor int64, limit int) ([]Page, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+selectColumns+`
		FROM contents c JOIN users u ON u.id = c.author_id
		WHERE `+publicFilter+` AND ($1 = 0 OR c.id < $1)
		ORDER BY c.id DESC LIMIT $2`, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("seo: list: %w", err)
	}
	defer rows.Close()
	result := []Page{}
	for rows.Next() {
		var raw rawRow
		if err := rows.Scan(&raw.id, &raw.slug, &raw.kind, &raw.title, &raw.description,
			&raw.handle, &raw.publishedAt, &raw.durationMS, &raw.manifestText); err != nil {
			return nil, err
		}
		page, err := r.build(ctx, raw)
		if err != nil {
			return nil, err
		}
		result = append(result, page)
	}
	return result, rows.Err()
}

func (r *Repository) Get(ctx context.Context, slug string) (Page, error) {
	var raw rawRow
	err := r.db.QueryRow(ctx, `
		SELECT `+selectColumns+`
		FROM contents c JOIN users u ON u.id = c.author_id
		WHERE `+publicFilter+` AND c.slug = $1`, slug).Scan(
		&raw.id, &raw.slug, &raw.kind, &raw.title, &raw.description,
		&raw.handle, &raw.publishedAt, &raw.durationMS, &raw.manifestText)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, err
	}
	return r.build(ctx, raw)
}

func (r *Repository) build(ctx context.Context, raw rawRow) (Page, error) {
	path := "/video/" + raw.slug
	if raw.kind == "image" {
		path = "/image/" + raw.slug
	}
	published := raw.publishedAt
	if published.Year() <= 1970 {
		published = time.Now().UTC()
	}
	title := raw.title
	if title == "" {
		title = "夜街作品 · " + raw.slug
	}
	page := Page{
		ID: raw.id, Slug: raw.slug, Type: raw.kind, Path: path,
		Canonical: r.siteURL + path, Title: title, Description: raw.description,
		Handle: raw.handle, Creator: "@" + raw.handle,
		PublishedAt: published.UTC().Format(time.RFC3339),
		DurationMS:  raw.durationMS, Indexable: true,
	}
	if raw.durationMS > 0 {
		page.DurationISO = fmt.Sprintf("PT%dS", raw.durationMS/1000)
	}

	if raw.manifestText != "" {
		var manifest struct {
			DurationMS int64 `json:"duration_ms"`
			Files      []struct {
				Key string `json:"key"`
			} `json:"files"`
		}
		if err := json.Unmarshal([]byte(raw.manifestText), &manifest); err == nil {
			if page.DurationMS == 0 && manifest.DurationMS > 0 {
				page.DurationMS = manifest.DurationMS
				page.DurationISO = fmt.Sprintf("PT%dS", manifest.DurationMS/1000)
			}
			for _, file := range manifest.Files {
				base := file.Key[strings.LastIndex(file.Key, "/")+1:]
				switch {
				case base == "poster.webp" || strings.HasPrefix(base, "cover-"):
					if page.ThumbnailURL == "" {
						page.ThumbnailURL = r.stableURL(file.Key)
					}
				case base == "master.m3u8":
					page.ContentURL = r.stableURL(file.Key)
				case page.ContentURL == "" && strings.HasSuffix(base, ".mp4") && base != "mezzanine.mp4":
					page.ContentURL = r.stableURL(file.Key)
				}
			}
		}
	}

	images, err := r.images(ctx, raw.id)
	if err != nil {
		return page, err
	}
	page.Images = images
	if len(images) > 0 && page.ThumbnailURL == "" {
		page.ThumbnailURL = images[0]
	}
	return page, nil
}

func (r *Repository) images(ctx context.Context, contentID int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.driver, s.object_key
		FROM media_assets m JOIN storage_objects s ON s.media_asset_id = m.id
		WHERE m.content_id = $1 AND m.kind = 'image'
		ORDER BY m.position, m.id`, contentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var driver, key string
		if err := rows.Scan(&driver, &key); err != nil {
			return nil, err
		}
		if driver == "s3" {
			// Sitemap URLs must be stable and crawlable: keep the CDN path here
			// instead of a short-lived presigned GET (production enables public read).
			key = r.store.PublicURL(key)
		} else if strings.HasPrefix(key, "/") {
			key = r.siteURL + key // dev-only static demo asset
		}
		result = append(result, key)
	}
	return result, rows.Err()
}

// stableURL never contains short-lived signatures: structured data and
// sitemaps must remain crawlable (开发技术规范 §11.2).
func (r *Repository) stableURL(key string) string {
	return r.store.PublicURL(key)
}
