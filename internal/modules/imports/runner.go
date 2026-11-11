// Package imports implements the remote-source Worker (开发技术规范 §4.5/§4.6).
// Media bytes are downloaded by the Worker with strict limits and handed to the
// existing M1 transcode pipeline via S3 — the API process never proxies media.
package imports

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

var ErrNoJob = errors.New("imports: no pending job")

const (
	maxRedirects = 3
	maxFileBytes = int64(2048) * 1024 * 1024
	downloadTTL  = 5 * time.Minute
)

type Runner struct {
	db      *pgxpool.Pool
	store   *objectstore.Store
	cfg     config.Config
	log     *slog.Logger
	tempDir string
	client  *http.Client
}

func NewRunner(db *pgxpool.Pool, store *objectstore.Store, cfg config.Config,
	log *slog.Logger) *Runner {
	runner := &Runner{
		db: db, store: store, cfg: cfg, log: log,
		tempDir: cfg.WorkerTempDir,
	}
	runner.client = &http.Client{
		Timeout: downloadTTL,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("imports: too many redirects")
			}
			// §4.6：重定向后重新校验目标地址。
			if err := admin.ValidateRemoteURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	return runner
}

func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			roundCtx, cancel := context.WithTimeout(ctx, downloadTTL+time.Minute)
			worked, err := r.RunOnce(roundCtx)
			cancel()
			if err != nil && !errors.Is(err, ErrNoJob) && r.log != nil {
				r.log.Error("import round failed", "error", err)
			}
			if worked {
				continue
			}
		}
	}
}

type job struct {
	ID             int64
	ConnectorID    int64
	ExternalID     string
	SourceURL      string
	IdempotencyKey string
}

func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	var item job
	err := r.db.QueryRow(ctx, `
		WITH candidate AS (
		  SELECT id FROM import_jobs WHERE status = 'pending'
		  ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE import_jobs j SET status = 'downloading', updated_at = now()
		FROM candidate c WHERE j.id = c.id
		RETURNING j.id, j.connector_id, j.external_id, j.source_url, j.idempotency_key`,
	).Scan(&item.ID, &item.ConnectorID, &item.ExternalID,
		&item.SourceURL, &item.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNoJob
	}
	if err != nil {
		return false, err
	}
	if err := r.process(ctx, item); err != nil {
		_, _ = r.db.Exec(context.Background(), `
			UPDATE import_jobs SET status = 'failed', last_error = $2, updated_at = now()
			WHERE id = $1`, item.ID, trimError(err))
		if r.log != nil {
			r.log.Warn("import job failed", "job", item.ID, "error", err)
		}
	}
	return true, nil
}

func trimError(err error) string {
	text := err.Error()
	if len(text) > 300 {
		return text[:300]
	}
	return text
}

func (r *Runner) process(ctx context.Context, item job) error {
	var slug, kind, status, baseURL, configText string
	var allowImport bool
	if err := r.db.QueryRow(ctx, `
		SELECT slug, kind, status, base_url, allow_import, config::text
		FROM source_connectors WHERE id = $1`, item.ConnectorID).Scan(
		&slug, &kind, &status, &baseURL, &allowImport, &configText); err != nil {
		return fmt.Errorf("imports: connector lookup: %w", err)
	}
	if status != "active" || !allowImport {
		return fmt.Errorf("imports: connector %s is not authorized for import", slug)
	}
	// 归属账号由服务端配置指定，普通用户不能通过提交 ID 冒充作者。
	ownerID := parseOwnerID(configText)
	if ownerID == 0 {
		return fmt.Errorf("imports: connector %s has no owner_user_id in config", slug)
	}

	address := item.SourceURL
	if address == "" {
		address = strings.TrimRight(baseURL, "/") + "/" + item.ExternalID
	}
	if err := admin.ValidateRemoteURL(address); err != nil {
		return err
	}
	if r.store == nil {
		return fmt.Errorf("imports: object storage is not configured")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := r.client.Do(request)
	if err != nil {
		return fmt.Errorf("imports: download: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("imports: remote returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxFileBytes || response.ContentLength > r.cfg.MaxUploadBytes {
		return fmt.Errorf("imports: remote file exceeds size limit")
	}

	if err := os.MkdirAll(r.tempDir, 0o755); err != nil {
		return err
	}
	temporary := filepath.Join(r.tempDir,
		fmt.Sprintf("import-%d-%s.bin", item.ID, shortToken()))
	file, err := os.Create(temporary)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	written, err := io.Copy(file, io.LimitReader(response.Body, maxFileBytes+1))
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("imports: write temp: %w", err)
	}
	if written > maxFileBytes {
		return fmt.Errorf("imports: streamed file exceeds %d bytes", maxFileBytes)
	}

	key, err := objectstore.SourceKey(ownerID)
	if err != nil {
		return err
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "video/mp4"
	}
	if _, err := r.store.UploadFile(ctx, key, temporary, contentType,
		"private, no-store"); err != nil {
		return fmt.Errorf("imports: upload to bucket: %w", err)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	contentSlug, err := shortSlug()
	if err != nil {
		return err
	}
	var contentID, uploadID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO contents
		  (slug, author_id, type, title, origin, visibility, publish_state, review_status, feed_eligible)
		VALUES ($1, $2, 'video', $3, 'external_imported', 'private', 'draft', 'pending', false)
		RETURNING id`, contentSlug, ownerID,
		"导入作品 "+item.ExternalID).Scan(&contentID); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO upload_jobs
		  (content_id, owner_id, idempotency_key, object_key, mime_type, byte_size, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'uploaded') RETURNING id`,
		contentID, ownerID, "import:"+item.IdempotencyKey, key, contentType,
		written).Scan(&uploadID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcode_jobs (upload_id, content_id, status)
		VALUES ($1, $2, 'uploaded')`, uploadID, contentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE import_jobs SET status = 'transcoding', content_id = $2,
		  byte_size = $3, last_error = '', updated_at = now() WHERE id = $1`,
		item.ID, contentID, written); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if r.log != nil {
		r.log.Info("import handed to transcode", "job", item.ID,
			"connector", slug, "content_id", contentID, "bytes", written)
	}
	return nil
}

func parseOwnerID(configText string) int64 {
	// config 是服务端保存的最小 JSON；只读取归属账号，不读取任何密钥。
	var config struct {
		OwnerUserID int64 `json:"owner_user_id"`
	}
	_ = json.Unmarshal([]byte(configText), &config)
	return config.OwnerUserID
}

func shortToken() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func shortSlug() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
