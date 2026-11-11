// Command worker runs the transcode pipeline. PostgreSQL job rows are the source of truth.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yejie/voidxotv/internal/modules/content"
	"github.com/yejie/voidxotv/internal/modules/delivery"
	"github.com/yejie/voidxotv/internal/modules/email"
	"github.com/yejie/voidxotv/internal/modules/imports"
	"github.com/yejie/voidxotv/internal/modules/stats"
	"github.com/yejie/voidxotv/internal/modules/transcode"
	"github.com/yejie/voidxotv/internal/platform/buildinfo"
	"github.com/yejie/voidxotv/internal/platform/cache"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/database"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
	"github.com/yejie/voidxotv/internal/platform/queue"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	cacheStore, err := cache.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer cacheStore.Close()
	store, err := objectstore.New(cfg)
	if err != nil {
		return err
	}
	if err := store.EnsureBucket(ctx); err != nil {
		log.Warn("bucket check failed", "error", err)
	}

	jobs := transcode.NewJobRepository(db)
	pipeline := transcode.NewPipeline(cfg, jobs, store, log)
	notifier := queue.NewNotifier(cacheStore.Client())
	runner := transcode.NewRunner(jobs, pipeline, notifier, cfg, log)

	// Delivery 是 Go 后端内的模块：同一个 Worker 进程内并行消费 Outbox 与 delivery_jobs。
	deliveryRepo := delivery.NewRepository(db)
	deliveryRunner := delivery.NewRunner(deliveryRepo,
		delivery.NewRevalidator(cfg), delivery.NewPurger(cfg, log), cfg, log)
	go func() {
		deliveryTicker := time.NewTicker(cfg.DeliveryIdleInterval)
		defer deliveryTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-deliveryTicker.C:
				roundCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				worked, err := deliveryRunner.RunOnce(roundCtx)
				cancel()
				if err != nil {
					log.Error("delivery round failed", "error", err)
				}
				if worked {
					continue
				}
			}
		}
	}()

	// M4：统计聚合（5 分钟）与远程导入 Worker 与转码/Delivery 同进程并行。
	go stats.NewAggregator(db, cacheStore.Client(), log).Run(ctx, cfg.StatsInterval)
	go imports.NewRunner(db, store, cfg, log).Run(ctx, cfg.ImportsInterval)

	// 定时发布执行器（§8）：到点的 scheduled 内容转 published 并写 content.published 事件。
	go content.NewScheduler(db, log).Run(ctx, 30*time.Second)

	// 邮件投递（§11.7）：消费 outbox 里的 email.send，经自建 SMTP 发出；
	// 未配置 SMTP 时事件直接进死信并留原因，不阻塞其他消费。
	emailSender := email.NewSender(cfg)
	go email.NewRunner(db, emailSender, cfg, log).Run(ctx, 5*time.Second)

	log.Info("worker started", "worker_id", cfg.WorkerID, "temp_dir", cfg.WorkerTempDir,
		"ffmpeg", cfg.FFmpegPath, "lease_ttl", cfg.WorkerLeaseTTL.String(),
		"version", buildinfo.Version, "commit", buildinfo.Commit)
	ticker := time.NewTicker(cfg.WorkerIdleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("worker stopped")
			return nil
		case <-ticker.C:
			roundCtx, cancel := context.WithTimeout(ctx, cfg.TranscodeTimeout+time.Minute)
			worked, err := runner.RunOnce(roundCtx)
			cancel()
			if err != nil {
				log.Error("worker round failed", "error", err)
			}
			if worked {
				// A completed job may have queued more work; scan again without waiting.
				continue
			}
		}
	}
}
