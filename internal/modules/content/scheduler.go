package content

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Scheduler 把到期的定时发布推上线（§8 发布时间：定时发布）。
// scheduled 只是「等待发布时刻」的状态；到点且审核已通过才转 published，
// 与手动发布同样在同一事务里写 content.published 事件，缓存失效走同一条 Outbox 链路。
type Scheduler struct {
	db  *pgxpool.Pool
	log *slog.Logger
}

func NewScheduler(db *pgxpool.Pool, log *slog.Logger) *Scheduler {
	return &Scheduler{db: db, log: log}
}

// Run 按固定周期扫描到期内容，直到 ctx 取消。
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			published, err := s.RunOnce(ctx)
			if err != nil {
				s.log.Error("scheduled publish round failed", "error", err)
			}
			if published > 0 {
				s.log.Info("scheduler published due contents", "count", published)
			}
		}
	}
}

// RunOnce 发布一批到期内容，返回本次发布的条数。
//   - review_status 必须已 approved：定时只是发布时机，不绕过审核（§6.1）；
//   - 作者被封禁时不发布：处置优先，解封后下一次扫描会补上；
//   - 事件键带内容 id + 纳秒时间戳，与手动发布的语义一致且天然幂等。
func (s *Scheduler) RunOnce(ctx context.Context) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		UPDATE contents c
		SET publish_state = 'published', published_at = now()
		WHERE c.publish_state = 'scheduled'
		  AND c.review_status = 'approved'
		  AND c.scheduled_at IS NOT NULL AND c.scheduled_at <= now()
		  AND EXISTS (SELECT 1 FROM users u WHERE u.id = c.author_id AND u.status = 'active')
		RETURNING c.id, c.slug`)
	if err != nil {
		return 0, err
	}
	type due struct {
		id   int64
		slug string
	}
	var dueList []due
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.id, &item.slug); err != nil {
			rows.Close()
			return 0, err
		}
		dueList = append(dueList, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(dueList) > 0 {
		// 一次写入所有到期内容的发布事件：积压时 SQL 次数仍是 1 条。
		eventKeys := make([]string, 0, len(dueList))
		ids := make([]int64, 0, len(dueList))
		slugs := make([]string, 0, len(dueList))
		for _, item := range dueList {
			eventKeys = append(eventKeys, fmt.Sprintf("content.published:%d:%d",
				item.id, time.Now().UnixNano()))
			ids = append(ids, item.id)
			slugs = append(slugs, item.slug)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			SELECT t.event_key, 'content.published',
			  jsonb_build_object('content_id', t.content_id, 'slug', t.slug)
			FROM unnest($1::text[], $2::bigint[], $3::text[])
			  AS t(event_key, content_id, slug)
			ON CONFLICT (event_key) DO NOTHING`, eventKeys, ids, slugs); err != nil {
			return 0, err
		}
	}
	if len(dueList) > 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, err
		}
		for _, item := range dueList {
			s.log.Info("scheduled content published", "content_id", item.id, "slug", item.slug)
		}
	}
	return len(dueList), nil
}
