package transcode

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/queue"
)

// Runner scans PostgreSQL first and treats Redis Stream wakeups only as hints.
type Runner struct {
	jobs     *JobRepository
	pipeline *Pipeline
	notifier *queue.Notifier
	cfg      config.Config
	log      *slog.Logger
}

func NewRunner(jobs *JobRepository, pipeline *Pipeline, notifier *queue.Notifier,
	cfg config.Config, log *slog.Logger) *Runner {
	return &Runner{jobs: jobs, pipeline: pipeline, notifier: notifier, cfg: cfg, log: log}
}

// leaseTimeout bounds only the claim step. Without it, a saturated connection
// pool would block the worker for the whole transcode timeout (observed as a
// worker that stops picking up newly enqueued jobs).
const leaseTimeout = 10 * time.Second

// RunOnce leases and executes at most one job; a lost wakeup can never hide recoverable work.
func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	job, err := r.lease(ctx)
	if errors.Is(err, ErrNoJob) {
		ids, readErr := r.notifier.ReadWakeups(ctx, r.cfg.WorkerID, 10)
		if readErr != nil || len(ids) == 0 {
			return false, nil
		}
		job, err = r.lease(ctx)
	}
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	r.log.Info("job leased", "job_id", job.ID, "attempt", job.AttemptCount, "worker", r.cfg.WorkerID)
	stop := make(chan struct{})
	go r.heartbeatLoop(ctx, job.ID, stop)
	executeErr := r.pipeline.Execute(ctx, job, r.cfg.WorkerID)
	close(stop)

	if executeErr == nil {
		r.log.Info("job published", "job_id", job.ID)
		return true, nil
	}
	r.log.Warn("job failed", "job_id", job.ID, "error", executeErr)

	message := sanitize(executeErr.Error())
	if ShouldRetry(job.AttemptCount, isPermanent(executeErr)) {
		next := time.Now().UTC().Add(RetryDelay(job.AttemptCount))
		if _, err := r.jobs.MarkRetry(ctx, job.ID, r.cfg.WorkerID, message, next); err != nil {
			return false, err
		}
		r.log.Info("job scheduled for retry", "job_id", job.ID, "next_attempt_at", next)
	} else {
		if _, err := r.jobs.MarkFailed(ctx, job.ID, r.cfg.WorkerID, message); err != nil {
			return false, err
		}
		r.log.Error("job permanently failed", "job_id", job.ID)
	}
	return false, executeErr
}

func (r *Runner) lease(ctx context.Context) (Job, error) {
	leaseCtx, cancel := context.WithTimeout(ctx, leaseTimeout)
	defer cancel()
	return r.jobs.LeaseNext(leaseCtx, r.cfg.WorkerID, r.cfg.WorkerLeaseTTL)
}

func (r *Runner) heartbeatLoop(ctx context.Context, jobID int64, stop <-chan struct{}) {
	ticker := time.NewTicker(r.cfg.WorkerHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			ok, err := r.jobs.Heartbeat(ctx, jobID, r.cfg.WorkerID, r.cfg.WorkerLeaseTTL)
			if err != nil || !ok {
				r.log.Warn("heartbeat lost lease", "job_id", jobID, "error", err)
			}
		}
	}
}

func isPermanent(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"probe source", "unsupported", "invalid duration", "no video stream",
		"differs from source", "playlist is incomplete", "no artifacts",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func sanitize(message string) string {
	if len(message) > 500 {
		return message[:500]
	}
	return message
}
