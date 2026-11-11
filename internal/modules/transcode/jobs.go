package transcode

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoJob = errors.New("no claimable job")

// Job is a PostgreSQL fact row, never a Redis queue item.
type Job struct {
	ID             int64
	UploadID       int64
	ContentID      int64
	ObjectKey      string
	AttemptCount   int
	MaxAttempts    int
	ProfileVersion string
	Status         string
	LastError      string
}

type JobRepository struct {
	db *pgxpool.Pool
}

func NewJobRepository(db *pgxpool.Pool) *JobRepository { return &JobRepository{db: db} }

// LeaseNext atomically claims one job with FOR UPDATE SKIP LOCKED; expired leases are reclaimable.
func (r *JobRepository) LeaseNext(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	leaseExpires := time.Now().UTC().Add(leaseTTL)
	const query = `
		WITH candidate AS (
			SELECT id FROM transcode_jobs
			WHERE status IN ('uploaded', 'probing', 'processing', 'packaging', 'verifying')
			  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
			  AND (lease_expires_at IS NULL OR lease_expires_at < now() OR lease_owner = $1)
			ORDER BY next_attempt_at NULLS FIRST, created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE transcode_jobs t
		SET status = 'probing', lease_owner = $1, lease_expires_at = $2,
		    attempt_count = t.attempt_count + 1, updated_at = now()
		FROM candidate c
		WHERE t.id = c.id
		RETURNING t.id, t.upload_id, t.content_id,
		          (SELECT u.object_key FROM upload_jobs u WHERE u.id = t.upload_id),
		          t.attempt_count, t.max_attempts, t.profile_version, t.status`
	var job Job
	err = tx.QueryRow(ctx, query, workerID, leaseExpires).Scan(
		&job.ID, &job.UploadID, &job.ContentID, &job.ObjectKey,
		&job.AttemptCount, &job.MaxAttempts, &job.ProfileVersion, &job.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, fmt.Errorf("lease job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Heartbeat extends the lease only while this worker still owns an unexpired lease.
func (r *JobRepository) Heartbeat(ctx context.Context, jobID int64, workerID string, leaseTTL time.Duration) (bool, error) {
	expires := time.Now().UTC().Add(leaseTTL)
	tag, err := r.db.Exec(ctx, `
		UPDATE transcode_jobs SET lease_expires_at = $3, updated_at = now()
		WHERE id = $1 AND lease_owner = $2
		  AND status IN ('probing', 'processing', 'packaging', 'verifying')
		  AND lease_expires_at IS NOT NULL AND lease_expires_at > now()`,
		jobID, workerID, expires)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *JobRepository) SetStatus(ctx context.Context, jobID int64, workerID, status string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE transcode_jobs SET status = $3, updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND lease_expires_at > now()`,
		jobID, workerID, status)
	return err
}

// MarkSucceeded publishes the manifest and sets retention dates in one transaction.
func (r *JobRepository) MarkSucceeded(ctx context.Context, job Job, workerID string,
	manifest []byte, durationMS int64, succeededAt time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE transcode_jobs
		SET status = 'published', lease_owner = NULL, lease_expires_at = NULL,
		    transcode_succeeded_at = $3::timestamptz,
		    source_expires_at = $3::timestamptz + interval '7 days',
		    mezzanine_expires_at = $3::timestamptz + interval '30 days',
		    last_error = NULL, updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND lease_expires_at > now()`,
		job.ID, workerID, succeededAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("lease lost before publish")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO artifact_manifests (job_id, content_id, profile_version, manifest)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (job_id) DO UPDATE SET manifest = EXCLUDED.manifest`,
		job.ID, job.ContentID, ProfileVersion, string(manifest)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE contents SET duration_ms = $2, updated_at = now() WHERE id = $1`,
		job.ContentID, durationMS); err != nil {
		return err
	}
	// Transactional Outbox: the content event and the manifest commit together,
	// so the Delivery scheduler can never miss a newly published asset set.
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_key, event_type, payload)
		VALUES ($1, 'content.assets_published',
		  jsonb_build_object('content_id', $2::bigint,
		    'slug', (SELECT slug FROM contents WHERE id = $2::bigint)))
		ON CONFLICT (event_key) DO NOTHING`,
		fmt.Sprintf("content.assets_published:%d", job.ID), job.ContentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkRetry returns the job to 'uploaded' with an exponential backoff (30s / 2m / 10m ±10%).
func (r *JobRepository) MarkRetry(ctx context.Context, jobID int64, workerID, message string,
	nextAttemptAt time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE transcode_jobs
		SET status = 'uploaded', lease_owner = NULL, lease_expires_at = NULL,
		    last_error = $3, next_attempt_at = $4, updated_at = now()
		WHERE id = $1 AND lease_owner = $2
		  AND status IN ('probing', 'processing', 'packaging', 'verifying')
		  AND lease_expires_at IS NOT NULL AND lease_expires_at > now()`,
		jobID, workerID, message, nextAttemptAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *JobRepository) MarkFailed(ctx context.Context, jobID int64, workerID, message string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE transcode_jobs
		SET status = 'transcode_failed', lease_owner = NULL, lease_expires_at = NULL,
		    last_error = $3, updated_at = now()
		WHERE id = $1 AND lease_owner = $2`, jobID, workerID, message)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RetryDelay mirrors the reference RetryPolicy (attempt 2/3/4 → 30s / 2m / 10m with ±10% jitter).
func RetryDelay(attempt int) time.Duration {
	base := 30 * time.Second
	switch attempt {
	case 3:
		base = 2 * time.Minute
	case 4:
		base = 10 * time.Minute
	}
	jitter := (rand.Float64()*2 - 1) * 0.1
	return time.Duration(float64(base) * (1 + jitter))
}

// ShouldRetry caps execution attempts at four, matching the reference plan.
func ShouldRetry(attempt int, permanent bool) bool {
	return !permanent && attempt < 4
}
