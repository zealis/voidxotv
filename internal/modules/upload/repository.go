package upload

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/content"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// CreateTicket persists a draft content and its upload row in one transaction.
// Replaying the same idempotency key returns the existing ticket.
// existingContentID > 0 时复用发布页已自动保存的草稿（技术规范 §6.1），
// 不另建 content；为 0 时保持 M1 行为（票据自带空草稿）。
func (r *Repository) CreateTicket(ctx context.Context, ownerID, existingContentID int64,
	idemKey, objectKey, multipartUploadID, contentType string, byteSize int64, partCount int) (
	uploadJobID, contentID int64, replay bool, err error) {

	var existingKey, existingMultipart string
	var existingParts int
	err = r.db.QueryRow(ctx, `
		SELECT id, content_id, object_key, multipart_upload_id, part_count
		FROM upload_jobs WHERE owner_id = $1 AND idempotency_key = $2`,
		ownerID, idemKey).Scan(&uploadJobID, &contentID, &existingKey, &existingMultipart, &existingParts)
	if err == nil {
		return uploadJobID, contentID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, fmt.Errorf("lookup upload ticket: %w", err)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existingContentID > 0 {
		// 必须是本人且仍是草稿，避免把票据挂到别人的或已发布的作品上。
		var state string
		if err := tx.QueryRow(ctx, `
			SELECT publish_state FROM contents
			WHERE id = $1 AND author_id = $2 FOR UPDATE`, existingContentID, ownerID).Scan(&state); err != nil {
			return 0, 0, false, fmt.Errorf("%w: 草稿不存在或不属于当前用户", ErrInvalidInput)
		}
		if state != "draft" {
			return 0, 0, false, fmt.Errorf("%w: 只有草稿可以关联上传票据", ErrInvalidInput)
		}
		contentID = existingContentID
	} else {
		slug, err := content.NewSlug()
		if err != nil {
			return 0, 0, false, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO contents (slug, author_id, type, title, description)
			VALUES ($1, $2, 'video', '', '') RETURNING id`, slug, ownerID).Scan(&contentID); err != nil {
			return 0, 0, false, fmt.Errorf("create draft content: %w", err)
		}
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO upload_jobs
			(content_id, owner_id, idempotency_key, object_key, mime_type, byte_size,
			 status, multipart_upload_id, part_count)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8) RETURNING id`,
		contentID, ownerID, idemKey, objectKey, contentType, byteSize,
		multipartUploadID, partCount).Scan(&uploadJobID); err != nil {
		return 0, 0, false, fmt.Errorf("create upload job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, false, err
	}
	return uploadJobID, contentID, false, nil
}

// FindByIdempotency replays an existing ticket instead of creating an orphan multipart upload.
func (r *Repository) FindByIdempotency(ctx context.Context, ownerID int64, idemKey string) (
	uploadJobID, contentID int64, objectKey, multipartID string, declaredSize int64, partCount int, status string, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT id, content_id, object_key, multipart_upload_id, byte_size, part_count, status
		FROM upload_jobs WHERE owner_id = $1 AND idempotency_key = $2`, ownerID, idemKey).
		Scan(&uploadJobID, &contentID, &objectKey, &multipartID, &declaredSize, &partCount, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// LoadPending returns the ticket data needed to sign parts or complete the upload.
func (r *Repository) Load(ctx context.Context, uploadJobID, ownerID int64) (
	contentID int64, objectKey, multipartID string, declaredSize int64, partCount int, status string, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT content_id, object_key, multipart_upload_id, byte_size, part_count, status
		FROM upload_jobs WHERE id = $1 AND owner_id = $2`, uploadJobID, ownerID).
		Scan(&contentID, &objectKey, &multipartID, &declaredSize, &partCount, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// Complete transitions the upload and creates (or replays) the transcode job atomically.
func (r *Repository) Complete(ctx context.Context, uploadJobID, ownerID, actualSize int64) (
	transcodeJobID, contentID int64, err error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx, `
		SELECT content_id, status FROM upload_jobs
		WHERE id = $1 AND owner_id = $2 FOR UPDATE`, uploadJobID, ownerID).
		Scan(&contentID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE upload_jobs SET status = 'uploaded', byte_size = $2, updated_at = now()
		WHERE id = $1`, uploadJobID, actualSize); err != nil {
		return 0, 0, err
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO transcode_jobs (upload_id, content_id, status, next_attempt_at)
		VALUES ($1, $2, 'uploaded', now())
		ON CONFLICT (upload_id) DO NOTHING RETURNING id`, uploadJobID, contentID).Scan(&transcodeJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM transcode_jobs WHERE upload_id = $1`, uploadJobID).
			Scan(&transcodeJobID)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("create transcode job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return transcodeJobID, contentID, nil
}

func (r *Repository) Status(ctx context.Context, uploadJobID, ownerID int64) (Status, error) {
	var result Status
	result.UploadJobID = uploadJobID
	err := r.db.QueryRow(ctx, `
		SELECT u.content_id, u.status,
		       COALESCE(t.id, 0), COALESCE(t.status, ''), COALESCE(t.last_error, '')
		FROM upload_jobs u
		LEFT JOIN transcode_jobs t ON t.upload_id = u.id
		WHERE u.id = $1 AND u.owner_id = $2`, uploadJobID, ownerID).
		Scan(&result.ContentID, &result.UploadStatus,
			&result.TranscodeJobID, &result.TranscodeStatus, &result.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, ErrNotFound
	}
	return result, err
}
