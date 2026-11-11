package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
)

// Runner drains the Outbox into delivery_jobs and executes claimable jobs.
// PostgreSQL is the source of truth; Redis is not needed for this small queue.
type Runner struct {
	repo        *Repository
	revalidator *Revalidator
	purger      Purger
	cfg         config.Config
	log         *slog.Logger
	workerID    string
}

func NewRunner(repo *Repository, revalidator *Revalidator, purger Purger,
	cfg config.Config, log *slog.Logger) *Runner {
	return &Runner{
		repo: repo, revalidator: revalidator, purger: purger, cfg: cfg,
		log: log, workerID: "delivery-" + cfg.WorkerID,
	}
}

func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	worked := false

	event, err := r.repo.ClaimOutbox(ctx)
	if err == nil {
		worked = true
		if err := r.handleOutbox(ctx, event); err != nil {
			r.log.Warn("delivery: outbox event deferred", "event", event.Key, "error", err)
		}
	} else if !errors.Is(err, ErrNoOutbox) {
		return worked, err
	}

	job, err := r.repo.ClaimJob(ctx, r.workerID, r.cfg.DeliveryLeaseTTL)
	if err == nil {
		worked = true
		r.handleJob(ctx, job)
	} else if !errors.Is(err, ErrNoJob) {
		return worked, err
	}
	return worked, nil
}

func (r *Runner) handleOutbox(ctx context.Context, event OutboxEvent) error {
	ref, err := r.repo.ResolveContent(ctx, event.Payload)
	if err != nil {
		// 内容已被物理删除：重试不会有结果，直接进入终态。
		// 软删除不会走到这里（记录仍在，只改 publish_state），
		// 因此这条分支只对应合规硬删 / 运维清理。
		if errors.Is(err, ErrContentGone) {
			_ = r.repo.MarkOutboxFailed(ctx, event.ID, err)
			// 硬删自动回收：detail 预渲染页置 recycled，无需再手动失效。
			if recycleErr := r.repo.RecycleContentPages(ctx, ref.Slug, ref.Type); recycleErr != nil {
				r.log.Warn("delivery: recycle detail page failed", "slug", ref.Slug, "error", recycleErr)
			}
			r.repo.Log(ctx, 0, "", "outbox",
				fmt.Sprintf("%s → 内容已不存在，事件终止，预渲染页已自动回收", event.Type), false)
			r.log.Warn("delivery: outbox dropped, content no longer exists",
				"event", event.Key, "error", err)
			return nil
		}
		_ = r.repo.MarkOutboxRetry(ctx, event.ID, err, r.cfg.OutboxMaxAttempts)
		return err
	}
	if err := r.repo.Schedule(ctx, event, ref, r.cfg.DeliveryDebounce,
		r.cfg.DeliveryHomeDebounce, r.cfg.DeliveryMaxAttempts); err != nil {
		_ = r.repo.MarkOutboxRetry(ctx, event.ID, err, r.cfg.OutboxMaxAttempts)
		return err
	}
	if err := r.repo.MarkOutboxDone(ctx, event.ID); err != nil {
		return err
	}
	r.repo.Log(ctx, 0, "", "outbox", fmt.Sprintf("%s → %s (%s)", event.Type, ref.Slug, ref.Handle), true)
	return nil
}

func (r *Runner) handleJob(ctx context.Context, job Job) {
	paths := expandPaths(job.PageKey)
	workerID := r.workerID

	result, err := r.revalidator.Revalidate(ctx, paths)
	if err != nil {
		r.repo.Log(ctx, job.ID, job.PageKey, "revalidate", err.Error(), false)
		_ = r.repo.Retry(ctx, job, workerID, err)
		r.log.Warn("delivery: revalidate failed", "job", job.ID, "page", job.PageKey, "error", err)
		return
	}
	r.repo.Log(ctx, job.ID, job.PageKey, "revalidate",
		fmt.Sprintf("removed=%d warmed=%d", len(result.Removed), len(result.Warmed)), true)

	// CDN invalidation is isolated: its failure must not roll back publication.
	purgeURLs := make([]string, 0, len(paths))
	for _, path := range paths {
		purgeURLs = append(purgeURLs, strings.TrimRight(r.cfg.SiteURL, "/")+path)
	}
	if err := r.purger.Purge(ctx, purgeURLs); err != nil {
		r.repo.Log(ctx, job.ID, job.PageKey, "cdn_purge", err.Error(), false)
		r.log.Warn("delivery: cdn purge failed (will retry independently)", "job", job.ID, "error", err)
	} else {
		r.repo.Log(ctx, job.ID, job.PageKey, "cdn_purge", r.purger.Name(), true)
	}

	stale, err := r.repo.Publish(ctx, job, workerID)
	if err != nil {
		_ = r.repo.Retry(ctx, job, workerID, err)
		r.log.Warn("delivery: publish failed", "job", job.ID, "error", err)
		return
	}
	if stale {
		page, err := r.repo.GetPage(ctx, job.PageKey)
		if err == nil {
			_ = r.repo.Reschedule(ctx, job.PageKey, job.PageType, "version_catchup",
				page.DesiredVersion, r.cfg.DeliveryDebounce, r.cfg.DeliveryMaxAttempts)
			r.repo.Log(ctx, job.ID, job.PageKey, "reschedule",
				fmt.Sprintf("desired_version=%d > published=%d", page.DesiredVersion, job.TargetVersion), true)
		}
	}
	r.log.Info("delivery: page revalidated", "job", job.ID, "page", job.PageKey,
		"version", job.TargetVersion, "purger", r.purger.Name())
}

func expandPaths(pageKey string) []string {
	switch pageKey {
	case "sitemap:all":
		return []string{"/sitemap.xml", "/sitemap-videos.xml", "/sitemap-images.xml"}
	case "/":
		return []string{"/"}
	default:
		return []string{pageKey}
	}
}

func Backoff(attempt int) time.Duration {
	return time.Duration(1<<min(attempt, 5)) * time.Second
}
