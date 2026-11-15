package stats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// Aggregator folds Redis counters into PostgreSQL aggregate tables every five
// minutes. Failures leave the previous period intact — never a misleading zero.
type Aggregator struct {
	db         *pgxpool.Pool
	counters   *Counters
	client     *goredis.Client
	log        *slog.Logger
	playWeight float64
	likeWeight float64
}

func NewAggregator(db *pgxpool.Pool, client *goredis.Client, log *slog.Logger) *Aggregator {
	return &Aggregator{
		db: db, counters: NewCounters(client), client: client, log: log,
		playWeight: 1, likeWeight: 3,
	}
}

const aggregateLockKey = "yejie:stats:aggregate:lock"

// loadWeights 从 system_settings 读热门榜权重（§7.6）。
func (a *Aggregator) loadWeights(ctx context.Context) (float64, float64, error) {
	rows, err := a.db.Query(ctx, `
		SELECT key, value FROM system_settings
		WHERE key IN ('hot.play_weight', 'hot.like_weight')`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	play, like := a.playWeight, a.likeWeight
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return 0, 0, err
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 || parsed > 1000 {
			continue
		}
		switch key {
		case "hot.play_weight":
			play = parsed
		case "hot.like_weight":
			like = parsed
		}
	}
	return play, like, rows.Err()
}

// RunOnce is idempotent: Redis counters are cumulative (not consumed), and all
// PostgreSQL writes are UPSERTs keyed by a fixed time bucket.
func (a *Aggregator) RunOnce(ctx context.Context) error {
	// 权重每轮从 system_settings 读取（§7.6 运行时可调）；读失败沿用构造时的默认值。
	playWeight, likeWeight, err := a.loadWeights(ctx)
	if err != nil {
		a.log.Warn("stats: hot weights load failed, using defaults", "error", err)
	} else {
		a.playWeight, a.likeWeight = playWeight, likeWeight
	}
	locked, err := a.client.SetNX(ctx, aggregateLockKey, "1", 2*time.Minute).Result()
	if err != nil {
		return fmt.Errorf("stats: acquire lock: %w", err)
	}
	if !locked {
		return nil // another process is already aggregating
	}
	defer func() { _ = a.client.Del(ctx, aggregateLockKey).Err() }()

	bucket := time.Now().UTC().Truncate(5 * time.Minute)
	plays, err := a.counters.PlayCount(ctx, bucket)
	if err != nil {
		return err
	}
	contentPlays, err := a.counters.ContentPlayCounters(ctx)
	if err != nil {
		return err
	}

	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 首次运行时回填近 7 日的账号/内容/举报真实增量；播放无历史明细，保持 0
	// 并在概览页标注「播放自事件计数启用后统计」，不以 0 冒充有效播放量。
	if _, err := tx.Exec(ctx, `
		INSERT INTO stats_daily (day, plays, new_users, new_contents, new_reports, resolved_reports)
		SELECT d::date, 0,
		  (SELECT count(*) FROM users
		   WHERE created_at >= d AND created_at < d + interval '1 day'),
		  (SELECT count(*) FROM contents
		   WHERE created_at >= d AND created_at < d + interval '1 day'),
		  (SELECT count(*) FROM reports
		   WHERE created_at >= d AND created_at < d + interval '1 day'),
		  (SELECT count(*) FROM reports
		   WHERE resolved_at >= d AND resolved_at < d + interval '1 day')
		FROM generate_series(current_date - interval '6 days', current_date,
		                     interval '1 day') AS d
		ON CONFLICT (day) DO NOTHING`); err != nil {
		return fmt.Errorf("stats: backfill daily: %w", err)
	}

	var historicalPlays int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(plays), 0) FROM stats_overview_5m
		WHERE bucket < $1`, bucket).Scan(&historicalPlays); err != nil {
		return err
	}

	var newUsers, newContents, newReports, resolvedReports, pendingReviews int64
	if err := tx.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM users
		   WHERE created_at >= $1 AND created_at < $1 + interval '5 minutes'),
		  (SELECT count(*) FROM contents
		   WHERE created_at >= $1 AND created_at < $1 + interval '5 minutes'),
		  (SELECT count(*) FROM reports
		   WHERE created_at >= $1 AND created_at < $1 + interval '5 minutes'),
		  (SELECT count(*) FROM reports
		   WHERE resolved_at >= $1 AND resolved_at < $1 + interval '5 minutes'),
		  (SELECT count(*) FROM contents
		   WHERE review_status = 'pending' AND publish_state = 'published')`,
		bucket).Scan(&newUsers, &newContents, &newReports,
		&resolvedReports, &pendingReviews); err != nil {
		return fmt.Errorf("stats: window counters: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO stats_overview_5m
		  (bucket, plays, new_users, new_contents, new_reports, resolved_reports,
		   pending_reviews, total_plays)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (bucket) DO UPDATE SET
		  plays = EXCLUDED.plays, new_users = EXCLUDED.new_users,
		  new_contents = EXCLUDED.new_contents, new_reports = EXCLUDED.new_reports,
		  resolved_reports = EXCLUDED.resolved_reports,
		  pending_reviews = EXCLUDED.pending_reviews,
		  total_plays = EXCLUDED.total_plays, created_at = now()`,
		bucket, plays, newUsers, newContents, newReports, resolvedReports,
		pendingReviews, historicalPlays+plays); err != nil {
		return fmt.Errorf("stats: write 5m bucket: %w", err)
	}

	// 日汇总由 5 分钟桶重新求和，重复执行不会重复累加。
	if _, err := tx.Exec(ctx, `
		INSERT INTO stats_daily (day, plays, new_users, new_contents, new_reports, resolved_reports)
		SELECT bucket::date, sum(plays), sum(new_users), sum(new_contents),
		       sum(new_reports), sum(resolved_reports)
		FROM stats_overview_5m GROUP BY bucket::date
		ON CONFLICT (day) DO UPDATE SET
		  plays = EXCLUDED.plays, new_users = EXCLUDED.new_users,
		  new_contents = EXCLUDED.new_contents, new_reports = EXCLUDED.new_reports,
		  resolved_reports = EXCLUDED.resolved_reports, updated_at = now()`); err != nil {
		return fmt.Errorf("stats: fold daily: %w", err)
	}

	if len(contentPlays) > 0 {
		// 一次锁回现有计数、一次写当天增量、一次 upsert 累计值：
		// 聚合跑全量内容，逐条查询会随内容量线性增长。
		ids := make([]int64, 0, len(contentPlays))
		counts := make([]int64, 0, len(contentPlays))
		for contentID, count := range contentPlays {
			ids = append(ids, contentID)
			counts = append(counts, count)
		}

		rows, err := tx.Query(ctx, `
			SELECT content_id, plays FROM content_counters
			WHERE content_id = ANY($1) FOR UPDATE`, ids)
		if err != nil {
			return fmt.Errorf("stats: read content counters: %w", err)
		}
		previous := make(map[int64]int64, len(ids))
		for rows.Next() {
			var contentID, plays int64
			if err := rows.Scan(&contentID, &plays); err != nil {
				rows.Close()
				return fmt.Errorf("stats: scan content counter: %w", err)
			}
			previous[contentID] = plays
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("stats: read content counters: %w", err)
		}
		rows.Close()

		// 把本次累计值的增量记到当天，创作者看板才有按天趋势。
		// 首次见到某个作品时只建立基线、不记当天：否则历史累计会在第一次
		// 折叠时全部堆到今天，趋势图第一根柱子会假高。
		deltaIDs := make([]int64, 0, len(ids))
		deltas := make([]int64, 0, len(ids))
		for i, contentID := range ids {
			if before, ok := previous[contentID]; ok && counts[i] > before {
				deltaIDs = append(deltaIDs, contentID)
				deltas = append(deltas, counts[i]-before)
			}
		}
		if len(deltaIDs) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO content_stats_daily (content_id, day, plays)
				SELECT t.content_id, (now() AT TIME ZONE 'Asia/Shanghai')::date, t.plays
				FROM unnest($1::bigint[], $2::bigint[]) AS t(content_id, plays)
				ON CONFLICT (content_id, day) DO UPDATE
				  SET plays = content_stats_daily.plays + EXCLUDED.plays,
				      updated_at = now()`, deltaIDs, deltas); err != nil {
				return fmt.Errorf("stats: content daily plays: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO content_counters (content_id, plays)
			SELECT t.content_id, t.plays
			FROM unnest($1::bigint[], $2::bigint[]) AS t(content_id, plays)
			ON CONFLICT (content_id) DO UPDATE
			  SET plays = EXCLUDED.plays, updated_at = now()`, ids, counts); err != nil {
			return fmt.Errorf("stats: content counters: %w", err)
		}
	}

	var lastComputed time.Time
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(computed_at), 'epoch'::timestamptz) FROM hot_contents`).Scan(&lastComputed); err != nil {
		return err
	}
	if time.Since(lastComputed) >= time.Hour {
		if err := a.recomputeHot(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if a.log != nil {
		a.log.Info("stats aggregated", "bucket", bucket, "plays", plays,
			"pending_reviews", pendingReviews)
	}
	return nil
}

// recomputeHot rebuilds the 7-day weighted TOP list hourly (§7.6). It only
// contains public, approved, feed-eligible and recommendable content, so a
// takedown or "限流" decision removes an item from the list immediately.
func (a *Aggregator) recomputeHot(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM hot_contents`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		WITH base AS (
		  SELECT c.id,
		         COALESCE(cc.plays, 0)::bigint AS plays,
		         (SELECT count(*)::bigint FROM reactions r
		          WHERE r.content_id = c.id AND r.kind = 'like') AS likes
		  FROM contents c
		  LEFT JOIN content_counters cc ON cc.content_id = c.id
		  WHERE c.publish_state = 'published' AND c.review_status = 'approved'
		    AND c.visibility = 'public' AND c.feed_eligible AND c.recommend_eligible
		), ranked AS (
		  SELECT id, plays, likes,
		         (plays * $1::numeric + likes * $2::numeric) AS score
		  FROM base ORDER BY score DESC, id ASC LIMIT 20
		)
		INSERT INTO hot_contents (content_id, score, plays, likes, rank_no, computed_at)
		SELECT id, score, plays, likes,
		       row_number() OVER (ORDER BY score DESC, id ASC)::int, now()
		FROM ranked`, a.playWeight, a.likeWeight)
	if err != nil {
		return fmt.Errorf("stats: recompute hot: %w", err)
	}
	return nil
}

// Run drives the 5-minute cadence inside the Worker process.
func (a *Aggregator) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	run := func() {
		roundCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if err := a.RunOnce(roundCtx); err != nil && !errors.Is(err, context.Canceled) {
			if a.log != nil {
				a.log.Error("stats aggregation failed", "error", err)
			}
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
