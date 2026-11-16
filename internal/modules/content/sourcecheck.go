package content

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/modules/notifications"
)

// SourceChecker 周期探测去中心化（external_direct）视频的在线地址：
// 直链可能被源站下架 / 改链，观众无法自知。连续失败达到阈值会把作品
// 暂时移出推荐流并通知作者一次；恢复可访问后自动回到推荐流并再次通知。
type SourceChecker struct {
	db     *pgxpool.Pool
	log    *slog.Logger
	client *http.Client
}

const (
	// sourceFailThreshold：连续失败达到该次数才判定失效并移出推荐流，
	// 单次网络抖动 / 源站瞬断不直接下架。
	sourceFailThreshold = 3
	// sourceProbeTimeout：单条在线地址的探测超时。
	sourceProbeTimeout = 10 * time.Second
	// sourceBatch：单轮最多探测条数（最久未检测优先），把外呼压力摊到多个周期。
	sourceBatch = 100
	// sourceProbeConcurrency：单轮内并发的探测协程数。
	sourceProbeConcurrency = 5
	// sourceInterval 是 worker 接线时的默认周期（cmd/worker/main.go）。
	sourceInterval = 30 * time.Minute
)

func NewSourceChecker(db *pgxpool.Pool, log *slog.Logger) *SourceChecker {
	return &SourceChecker{
		db:     db,
		log:    log,
		client: &http.Client{Timeout: sourceProbeTimeout},
	}
}

// Run 按固定周期探测，直到 ctx 取消。启动即先跑一轮：进程重启可以立刻补检。
func (s *SourceChecker) Run(ctx context.Context, interval time.Duration) {
	if _, _, err := s.RunOnce(ctx); err != nil {
		s.log.Error("source check round failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checked, dead, err := s.RunOnce(ctx)
			if err != nil {
				s.log.Error("source check round failed", "error", err)
				continue
			}
			if dead > 0 {
				s.log.Warn("source check found newly dead videos", "checked", checked, "newly_dead", dead)
			}
		}
	}
}

type sourceTarget struct {
	ID       int64
	Title    string
	AuthorID int64
	URL      string
	Fails    int
	Eligible bool
	Approved bool
}

// RunOnce 探测一批最久未检测的在线地址作品：
//   - 成功：清零失败计数；曾被移出推荐流的（审核已通过）自动恢复并通知作者；
//   - 失败：连续失败计数 +1；达到阈值退出推荐流并通知作者一次。
//
// 单轮最多 sourceBatch 条（最久未检测优先），并发 sourceProbeConcurrency。
func (s *SourceChecker) RunOnce(ctx context.Context) (checked int, newlyDead int, err error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, COALESCE(title, ''), author_id, source_url,
		       source_fail_count, feed_eligible, review_status = 'approved'
		FROM contents
		WHERE origin = 'external_direct' AND type = 'video'
		  AND publish_state = 'published'
		ORDER BY source_checked_at NULLS FIRST, id
		LIMIT $1`, sourceBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("source check: list: %w", err)
	}
	defer rows.Close()

	var targets []sourceTarget
	for rows.Next() {
		var t sourceTarget
		if err := rows.Scan(&t.ID, &t.Title, &t.AuthorID, &t.URL,
			&t.Fails, &t.Eligible, &t.Approved); err != nil {
			return 0, 0, fmt.Errorf("source check: scan: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(targets) == 0 {
		return 0, 0, nil
	}

	// 并发探测；结果按 targets 顺序回填，落库阶段串行走同一连接池。
	results := make([]bool, len(targets))
	sem := make(chan struct{}, sourceProbeConcurrency)
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			results[i] = probeDirectVideo(ctx, s.client, targets[i].URL)
		}(i)
	}
	wg.Wait()

	for i, target := range targets {
		if err := ctx.Err(); err != nil {
			return checked, newlyDead, err
		}
		if results[i] {
			if err := s.applyRecovery(ctx, target); err != nil {
				s.log.Warn("source check: apply recovery", "content_id", target.ID, "error", err)
				continue
			}
			checked++
			continue
		}
		crossed, applyErr := s.applyFailure(ctx, target)
		if applyErr != nil {
			s.log.Warn("source check: apply failure", "content_id", target.ID, "error", applyErr)
			continue
		}
		checked++
		if crossed {
			newlyDead++
			s.notifyAuthor(ctx, target.AuthorID, target.Title, false)
		}
	}
	return checked, newlyDead, nil
}

// applyRecovery 成功且曾达阈值：清零计数、恢复推荐流资格，并通知作者恢复。
func (s *SourceChecker) applyRecovery(ctx context.Context, target sourceTarget) error {
	if target.Fails >= sourceFailThreshold && !target.Eligible && target.Approved {
		if _, err := s.db.Exec(ctx, `
			UPDATE contents SET source_fail_count = 0, source_checked_at = now(), feed_eligible = true
			WHERE id = $1`, target.ID); err != nil {
			return err
		}
		s.notifyAuthor(ctx, target.AuthorID, target.Title, true)
		return nil
	}
	_, err := s.db.Exec(ctx, `
		UPDATE contents SET source_fail_count = 0, source_checked_at = now()
		WHERE id = $1`, target.ID)
	return err
}

// applyFailure 计数 +1；跨越阈值且审核已通过时退出推荐流。
// 返回 crossed = 本次是否刚好跨过阈值（用于只通知一次）。
func (s *SourceChecker) applyFailure(ctx context.Context, target sourceTarget) (bool, error) {
	newFails := target.Fails + 1
	crossed := newFails >= sourceFailThreshold && target.Fails < sourceFailThreshold
	pull := crossed && target.Eligible && target.Approved
	_, err := s.db.Exec(ctx, `
		UPDATE contents SET source_fail_count = $2, source_checked_at = now(),
		feed_eligible = CASE WHEN $3 THEN false ELSE feed_eligible END
		WHERE id = $1`, target.ID, newFails, pull)
	return crossed, err
}

func (s *SourceChecker) notifyAuthor(ctx context.Context, authorID int64, title string, recovered bool) {
	msg := notifications.Message{
		UserID:   authorID,
		Kind:     "link",
		Category: notifications.KindSystem,
	}
	if recovered {
		msg.Title = "在线地址已恢复"
		msg.Body = fmt.Sprintf("《%s》的外部视频地址恢复可访问，作品已回到推荐流。", title)
	} else {
		msg.Title = "在线地址视频失效"
		msg.Body = fmt.Sprintf("《%s》的外部视频地址连续 %d 次检测失败，已暂时移出推荐流；请确认地址有效后重新发布。", title, sourceFailThreshold)
	}
	if _, err := notifications.Dispatch(ctx, s.db, msg); err != nil {
		s.log.Warn("source check: notify author", "title", title, "error", err)
	}
}

// probeDirectVideo 用 Range GET 探测在线地址：部分源站对 HEAD 表现异常，
// 拉 1 字节足以确认「可访问且可按范围读取」。2xx / 3xx 都算可达。
func probeDirectVideo(ctx context.Context, client *http.Client, raw string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "YejieSourceChecker/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}
