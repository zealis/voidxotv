package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNoOutbox = errors.New("no pending delivery outbox event")
	ErrNoJob    = errors.New("no claimable delivery job")
	ErrNotFound = errors.New("delivery object not found")
	// ErrContentGone 表示事件指向的内容已不存在（合规硬删 / 运维清理）。
	// 这是永久性失败：重试多少次都不会成功，应当直接进入终态。
	ErrContentGone = errors.New("delivery: content no longer exists")
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func (r *Repository) ClaimOutbox(ctx context.Context) (OutboxEvent, error) {
	var event OutboxEvent
	var payload string
	err := r.db.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending' AND event_type LIKE 'content.%'
			  AND next_attempt_at <= now()
			ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_events o SET status = 'processing', attempts = o.attempts + 1
		FROM candidate c WHERE o.id = c.id
		RETURNING o.id, o.event_key, o.event_type, o.payload::text`).Scan(
		&event.ID, &event.Key, &event.Type, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxEvent{}, ErrNoOutbox
	}
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("delivery: claim outbox: %w", err)
	}
	event.Payload = []byte(payload)
	return event, nil
}

func (r *Repository) ResolveContent(ctx context.Context, payload []byte) (ContentRef, error) {
	var ref ContentRef
	if err := json.Unmarshal(payload, &ref); err != nil {
		return ref, fmt.Errorf("delivery: decode event payload: %w", err)
	}
	if ref.ID == 0 {
		return ref, errors.New("delivery: event payload has no content_id")
	}
	if err := r.db.QueryRow(ctx, `
		SELECT c.slug, c.type, u.handle
		FROM contents c JOIN users u ON u.id = c.author_id
		WHERE c.id = $1`, ref.ID).Scan(&ref.Slug, &ref.Type, &ref.Handle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ref, fmt.Errorf("%w: id=%d", ErrContentGone, ref.ID)
		}
		return ref, fmt.Errorf("delivery: resolve content: %w", err)
	}
	// 分类页是真实缓存页面（/category/** 规则 600s SWR），内容变更时必须按依赖失效。
	rows, err := r.db.Query(ctx, `
		SELECT cat.slug FROM content_categories cc
		JOIN categories cat ON cat.id = cc.category_id
		WHERE cc.content_id = $1 AND cat.status = 'available'`, ref.ID)
	if err != nil {
		return ref, fmt.Errorf("delivery: resolve categories: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return ref, fmt.Errorf("delivery: scan category: %w", err)
		}
		ref.Categories = append(ref.Categories, slug)
	}
	if err := rows.Err(); err != nil {
		return ref, fmt.Errorf("delivery: resolve categories: %w", err)
	}
	return ref, nil
}

// Schedule expands one content event into the affected page set and records
// deduplicated, debounced jobs plus the desired_version increment (§10.3/§10.4).
func (r *Repository) Schedule(ctx context.Context, event OutboxEvent, ref ContentRef,
	debounce, homeDebounce time.Duration, maxAttempts int) error {

	detail := "/video/" + ref.Slug
	if ref.Type == "image" {
		detail = "/image/" + ref.Slug
	}
	pages := []scheduledPage{
		{Key: detail, Kind: "detail", Delay: debounce},
		{Key: "/creator/" + ref.Handle, Kind: "creator", Delay: debounce},
		{Key: "/", Kind: "home", Delay: homeDebounce},
		{Key: "sitemap:all", Kind: "sitemap", Delay: debounce},
	}
	// 关联触发：内容变更会改变所属分类页的列表（§10.3 依赖关系）。
	// 分类变更事件还会带上旧分类，避免「改分类后旧分类页仍缓存」的漏失效。
	seen := map[string]bool{}
	add := func(slug string) {
		if slug == "" || seen[slug] {
			return
		}
		seen[slug] = true
		pages = append(pages, scheduledPage{
			Key: "/category/" + slug, Kind: "category", Delay: debounce,
		})
	}
	for _, category := range ref.Categories {
		add(category)
	}
	for _, category := range ref.PreviousCategories {
		add(category)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, page := range pages {
		if err := r.enqueuePage(ctx, tx, page.Key, page.Kind, event.Type,
			0, time.Now().UTC().Add(page.Delay), maxAttempts); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_dependencies (page_key, depends_on, kind)
		VALUES ($1, $2, 'content')
		ON CONFLICT (page_key, depends_on) DO NOTHING`, detail, fmt.Sprintf("content:%d", ref.ID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// enqueuePage increments desired_version, cancels the superseded pending job and
// inserts exactly one job for the new version. It is idempotent per version.
// RecycleContentPages 在内容物理删除后回收其预渲染页（§10.4）：
// detail 行置 recycled，不再参与失效调度；creator / home / sitemap 页仍真实
// 存在，照常轮换。payload 缺 slug 时按两种深链接前缀都尝试（不存在即 no-op）。
func (r *Repository) RecycleContentPages(ctx context.Context, slug, typ string) error {
	keys := make([]string, 0, 2)
	if slug != "" {
		if typ == "video" {
			keys = append(keys, "/video/"+slug)
		} else if typ == "image" {
			keys = append(keys, "/image/"+slug)
		} else {
			keys = append(keys, "/video/"+slug, "/image/"+slug)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `
		UPDATE delivery_pages SET status = 'recycled', updated_at = now()
		WHERE page_key = ANY($1) AND page_type = 'detail'`, keys)
	return err
}

func (r *Repository) enqueuePage(ctx context.Context, db executor, pageKey, pageType,
	trigger string, priority int, scheduledAt time.Time, maxAttempts int) error {
	var version int64
	err := db.QueryRow(ctx, `
		INSERT INTO delivery_pages (page_key, page_type, desired_version)
		VALUES ($1, $2, 1)
		ON CONFLICT (page_key) DO UPDATE
		  SET desired_version = delivery_pages.desired_version + 1,
		      status = 'active', updated_at = now()
		RETURNING desired_version`, pageKey, pageType).Scan(&version)
	if err != nil {
		return fmt.Errorf("delivery: bump desired_version: %w", err)
	}
	if _, err := db.Exec(ctx, `
		UPDATE delivery_jobs SET status = 'cancelled', updated_at = now()
		WHERE page_key = $1 AND status = 'pending'`, pageKey); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO delivery_jobs
		  (page_key, page_type, trigger_type, priority, target_version, scheduled_at, max_attempts)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (
		  SELECT 1 FROM delivery_jobs d
		  WHERE d.page_key = $1 AND d.target_version = $5
		    AND d.status IN ('pending','processing','retrying','published')
		)`, pageKey, pageType, trigger, priority, version, scheduledAt, maxAttempts)
	return err
}

type executor interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (r *Repository) MarkOutboxDone(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE outbox_events SET status='done', processed_at=now(), last_error=NULL WHERE id=$1`, id)
	return err
}

// MarkOutboxRetry 把可重试的失败放回队列，并按尝试次数指数退避。
// 达到 maxAttempts 后转为 failed 终态：Outbox 不能有「永远重试」的事件，
// 否则一条永久性错误会永久占用唯一的领取位（§10.4 收敛要求）。
func (r *Repository) MarkOutboxRetry(ctx context.Context, id int64, cause error, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	_, err := r.db.Exec(ctx, `
		UPDATE outbox_events SET
		  status = CASE WHEN attempts >= $3 THEN 'failed' ELSE 'pending' END,
		  last_error = $2,
		  processed_at = CASE WHEN attempts >= $3 THEN now() ELSE processed_at END,
		  next_attempt_at = CASE WHEN attempts >= $3 THEN next_attempt_at
		    ELSE now() + make_interval(secs => LEAST(600, 5 * power(2, LEAST(attempts, 7)))) END
		WHERE id = $1`, id, cause.Error(), maxAttempts)
	return err
}

// MarkOutboxFailed 直接把事件置为终态（永久性错误，不重试）。
func (r *Repository) MarkOutboxFailed(ctx context.Context, id int64, cause error) error {
	_, err := r.db.Exec(ctx, `
		UPDATE outbox_events SET status = 'failed', last_error = $2, processed_at = now()
		WHERE id = $1`, id, cause.Error())
	return err
}

func (r *Repository) ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, error) {
	var job Job
	err := r.db.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM delivery_jobs
			WHERE status IN ('pending','retrying') AND scheduled_at <= now()
			  AND (lease_expires_at IS NULL OR lease_expires_at < now() OR lease_owner = $1)
			ORDER BY priority DESC, scheduled_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE delivery_jobs j
		SET status='processing', lease_owner=$1, lease_expires_at=$2,
		    attempt_count=j.attempt_count+1, started_at=now(), updated_at=now()
		FROM candidate c WHERE j.id=c.id
		RETURNING j.id, j.page_key, j.page_type, j.trigger_type,
		          j.target_version, j.attempt_count, j.max_attempts`,
		workerID, time.Now().UTC().Add(leaseTTL)).Scan(
		&job.ID, &job.PageKey, &job.PageType, &job.TriggerType,
		&job.TargetVersion, &job.AttemptCount, &job.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, fmt.Errorf("delivery: claim job: %w", err)
	}
	return job, nil
}

// Publish applies the CAS version update. A newer desired_version means the
// just-confirmed run is already stale and another pass must be scheduled.
func (r *Repository) Publish(ctx context.Context, job Job, workerID string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs SET status='published', completed_at=now(),
		  lease_owner=NULL, lease_expires_at=NULL, last_error=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2`, job.ID, workerID); err != nil {
		return false, err
	}
	var desired int64
	err = tx.QueryRow(ctx, `
		UPDATE delivery_pages SET published_version=$2, last_revalidated_at=now(),
		  published_at=now(), updated_at=now()
		WHERE page_key=$1 AND published_version < $2
		RETURNING desired_version`, job.PageKey, job.TargetVersion).Scan(&desired)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("delivery: version CAS lost for %s", job.PageKey)
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_versions (page_key, job_id, version, outcome)
		VALUES ($1,$2,$3,'published')`, job.PageKey, job.ID, job.TargetVersion); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_logs (job_id, page_key, action, detail, ok)
		VALUES ($1,$2,'revalidate','published_version advanced',true)`,
		job.ID, job.PageKey); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return desired > job.TargetVersion, nil
}

func (r *Repository) Retry(ctx context.Context, job Job, workerID string, cause error) error {
	status := "retrying"
	if job.AttemptCount >= job.MaxAttempts {
		status = "dead_letter"
	}
	delay := time.Duration(1<<min(job.AttemptCount, 5)) * time.Second
	_, err := r.db.Exec(ctx, `
		UPDATE delivery_jobs SET status=$3, last_error=$4,
		  scheduled_at=now()+$5::interval, lease_owner=NULL, lease_expires_at=NULL,
		  updated_at=now() WHERE id=$1 AND lease_owner=$2`,
		job.ID, workerID, status, cause.Error(), fmt.Sprintf("%d seconds", int(delay.Seconds())))
	if err == nil {
		_, _ = r.db.Exec(ctx, `
			INSERT INTO delivery_versions (page_key, job_id, version, outcome)
			VALUES ($1,$2,$3,$4)`, job.PageKey, job.ID, job.TargetVersion, status)
	}
	return err
}

func (r *Repository) Reschedule(ctx context.Context, pageKey, pageType, trigger string,
	version int64, delay time.Duration, maxAttempts int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO delivery_jobs
		  (page_key, page_type, trigger_type, priority, target_version, scheduled_at, max_attempts)
		SELECT $1,$2,$3,5,$4,now()+$5::interval,$6
		WHERE NOT EXISTS (
		  SELECT 1 FROM delivery_jobs d WHERE d.page_key=$1 AND d.target_version=$4
		    AND d.status IN ('pending','processing','retrying','published')
		)`, pageKey, pageType, trigger, version,
		fmt.Sprintf("%d seconds", int(delay.Seconds())), maxAttempts)
	return err
}

func (r *Repository) Log(ctx context.Context, jobID int64, pageKey, action, detail string, ok bool) {
	_, _ = r.db.Exec(ctx, `
		INSERT INTO delivery_logs (job_id, page_key, action, detail, ok)
		VALUES ($1,$2,$3,$4,$5)`, jobID, pageKey, action, detail, ok)
}

func (r *Repository) ListJobs(ctx context.Context, limit int) ([]JobView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, page_key, page_type, trigger_type, status, target_version,
		       attempt_count, COALESCE(last_error,''), scheduled_at, completed_at
		FROM delivery_jobs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []JobView{}
	for rows.Next() {
		var item JobView
		if err := rows.Scan(&item.ID, &item.PageKey, &item.PageType, &item.TriggerType,
			&item.Status, &item.TargetVersion, &item.Attempts, &item.LastError,
			&item.ScheduledAt, &item.CompletedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) GetJob(ctx context.Context, id int64) (JobView, error) {
	var item JobView
	err := r.db.QueryRow(ctx, `
		SELECT id, page_key, page_type, trigger_type, status, target_version,
		       attempt_count, COALESCE(last_error,''), scheduled_at, completed_at
		FROM delivery_jobs WHERE id=$1`, id).Scan(
		&item.ID, &item.PageKey, &item.PageType, &item.TriggerType, &item.Status,
		&item.TargetVersion, &item.Attempts, &item.LastError, &item.ScheduledAt, &item.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobView{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) GetPage(ctx context.Context, pageKey string) (Page, error) {
	var page Page
	err := r.db.QueryRow(ctx, `
		SELECT page_key, page_type, desired_version, published_version,
		       published_at, last_revalidated_at, status
		FROM delivery_pages WHERE page_key=$1`, pageKey).Scan(
		&page.PageKey, &page.PageType, &page.DesiredVersion, &page.PublishedVersion,
		&page.PublishedAt, &page.LastRevalidated, &page.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	return page, err
}

func (r *Repository) RetryJob(ctx context.Context, id int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE delivery_jobs SET status='retrying', scheduled_at=now(),
		  last_error=NULL, updated_at=now()
		WHERE id=$1 AND status IN ('dead_letter','cancelled')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) CancelJob(ctx context.Context, id int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE delivery_jobs SET status='cancelled', updated_at=now()
		WHERE id=$1 AND status IN ('pending','retrying','processing')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) Enqueue(ctx context.Context, pageKeys []string, priority, maxAttempts int,
	delay time.Duration) (int, error) {
	created := 0
	for _, pageKey := range pageKeys {
		kind := "detail"
		switch {
		case pageKey == "/":
			kind = "home"
		case pageKey == "sitemap:all":
			kind = "sitemap"
		case len(pageKey) > 8 && pageKey[:8] == "/creator":
			kind = "creator"
		case len(pageKey) > 9 && pageKey[:9] == "/category":
			kind = "category"
		}
		var version int64
		if err := r.db.QueryRow(ctx, `
			INSERT INTO delivery_pages (page_key, page_type, desired_version)
			VALUES ($1,$2,1)
			ON CONFLICT (page_key) DO UPDATE
			  SET desired_version = delivery_pages.desired_version + 1, updated_at=now()
			RETURNING desired_version`, pageKey, kind).Scan(&version); err != nil {
			return created, err
		}
		tag, err := r.db.Exec(ctx, `
			INSERT INTO delivery_jobs
			  (page_key,page_type,trigger_type,priority,target_version,scheduled_at,max_attempts)
			SELECT $1,$2,'manual',$3,$4,$5,$6
			WHERE NOT EXISTS (
			  SELECT 1 FROM delivery_jobs d WHERE d.page_key=$1 AND d.target_version=$4
			    AND d.status IN ('pending','processing','retrying','published')
			)`, pageKey, kind, priority, version, time.Now().UTC().Add(delay), maxAttempts)
		if err != nil {
			return created, err
		}
		created += int(tag.RowsAffected())
	}
	return created, nil
}

func (r *Repository) Stats(ctx context.Context) (map[string]int64, error) {
	result := map[string]int64{}
	rows, err := r.db.Query(ctx, `SELECT status, count(*) FROM delivery_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		result[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var stale int64
	if err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM delivery_pages WHERE published_version < desired_version`).Scan(&stale); err == nil {
		result["stale_pages"] = stale
	}
	return result, rows.Err()
}
