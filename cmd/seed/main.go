// Command seed creates development-only admin accounts and demo operations data.
// It never runs in production builds and never stores plaintext credentials.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/stats"
	"github.com/yejie/voidxotv/internal/platform/cache"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/database"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		return errors.New("seed: refuses to run in production")
	}
	password := cfg.AdminBootstrapPassword
	if password == "" {
		return errors.New("seed: YJ_ADMIN_BOOTSTRAP_PASSWORD is required (dev only)")
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

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

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	// 后台登录用邮箱（0.17.0 起没有用户名）：开发号统一 yj_<role>@admin.local，
	// 与迁移 000017 给存量账号补的占位邮箱同一规则。
	for _, role := range []string{"super_admin", "reviewer", "operator", "readonly"} {
		email := "yj_" + role + "@admin.local"
		if _, err := db.Exec(ctx, `
			INSERT INTO admin_users (email, password_hash, role, status)
			VALUES ($1, $2, $3, 'active')
			ON CONFLICT DO NOTHING`, email, hash, role); err != nil {
			return fmt.Errorf("seed: admin account %s: %w", role, err)
		}
	}

	// 演示账号：使用已有用户作为作者/举报人，避免编造不存在的对象。
	var userID int64
	if err := db.QueryRow(ctx, `SELECT id FROM users ORDER BY id ASC LIMIT 1`).Scan(&userID); err != nil {
		return fmt.Errorf("seed: need at least one user: %w", err)
	}
	var contentID int64
	var slug string
	if err := db.QueryRow(ctx, `
		SELECT id, slug FROM contents
		WHERE publish_state = 'published' AND review_status = 'approved'
		ORDER BY id ASC LIMIT 1`).Scan(&contentID, &slug); err != nil {
		return fmt.Errorf("seed: need one approved content (run seed-demo.sql first): %w", err)
	}

	// 风险信号：机器检测与版权投诉，用于审核队列风险分与标签。
	if _, err := db.Exec(ctx, `
		INSERT INTO content_risk_signals (content_id, signal, level, note)
		SELECT $1, 'machine', 2, '演示：疑似搬运信号'
		WHERE NOT EXISTS (SELECT 1 FROM content_risk_signals
		                  WHERE content_id = $1 AND signal = 'machine')`, contentID); err != nil {
		return err
	}

	// 举报并单示例：同一对象 24 小时内两条举报合并为一个处理单元。
	var caseID int64
	if err := db.QueryRow(ctx, `
		INSERT INTO report_cases (object_type, object_id, report_count, opened_at)
		SELECT 'content', $1, 2, now()
		WHERE NOT EXISTS (
		  SELECT 1 FROM report_cases
		  WHERE object_type = 'content' AND object_id = $1
		    AND status = 'pending' AND opened_at > now() - interval '24 hours')
		RETURNING id`, contentID).Scan(&caseID); err != nil {
		// 已有并单或无返回值时忽略，保持幂等。
		caseID = 0
	}
	if caseID > 0 {
		for _, reason := range []string{"vulgar", "copyright"} {
			if _, err := db.Exec(ctx, `
				INSERT INTO reports (reporter_id, object_type, object_id, reason, detail,
				                    status, case_id)
				VALUES ($1, 'content', $2, $3, '演示举报', 'pending', $4)`,
				userID, contentID, reason, caseID); err != nil {
				return err
			}
		}
	}

	// 互动示例：数据库唯一约束保证重复运行幂等。
	if _, err := db.Exec(ctx, `
		INSERT INTO reactions (user_id, content_id, kind)
		VALUES ($1, $2, 'like') ON CONFLICT DO NOTHING`, userID, contentID); err != nil {
		return err
	}

	// Connector 示例：密钥只以 secret_ref 形式登记，前端只看到脱敏状态。
	if _, err := db.Exec(ctx, `
		INSERT INTO source_connectors
		  (slug, name, kind, base_url, status, config, secret_ref, allow_direct_play, allow_import)
		VALUES
		  ('demo-webdav', '演示 WebDAV 归档', 'webdav', '',
		   'unauthorized', jsonb_build_object('owner_user_id', $1::bigint),
		   'YJ_WEBDAV_PASSWORD', false, true),
		  ('demo-partner', '演示授权第三方', 'third_party_api', '',
		   'unauthorized', '{}'::jsonb, 'YJ_PARTNER_API_KEY', true, false)
		ON CONFLICT (slug) DO NOTHING`, userID); err != nil {
		return err
	}

	// 话题运营数据：一个可用话题 + 一个待审自建话题。
	if _, err := db.Exec(ctx, `
		INSERT INTO topics (slug, name, status, creator_id) VALUES
		  ('demo-night-walk', '夜行', 'available', $1),
		  ('demo-user-topic', '用户自建话题（待审）', 'pending', $1)
		ON CONFLICT (slug) DO NOTHING`, userID); err != nil {
		return err
	}

	// 数据一致性修复：早期 M1 发布的公开内容在人工审核通过后未同步 feed 标记。
	// 仅修正「已发布 + 已通过 + 公开」的行，不动任何草稿或私密内容。
	if _, err := db.Exec(ctx, `
		UPDATE contents SET feed_eligible = true, updated_at = now()
		WHERE publish_state = 'published' AND review_status = 'approved'
		  AND visibility = 'public' AND feed_eligible = false`); err != nil {
		return err
	}

	// 话题关联：让 /topic/demo-night-walk 有一个可验证的公开作品。
	if _, err := db.Exec(ctx, `
		INSERT INTO content_topics (content_id, topic_id)
		SELECT $1, id FROM topics WHERE slug = 'demo-night-walk'
		ON CONFLICT (content_id, topic_id) DO NOTHING`, contentID); err != nil {
		return err
	}

	// 分类与标签：让 /category/[slug] 与搜索有可验证的数据。
	// 分类基线由 000006 迁移下发，这里只做内容归集，按 id 取模分散到各分类。
	if _, err := db.Exec(ctx, `
		WITH cats AS (
		  SELECT id, (row_number() OVER (ORDER BY position, id) - 1) AS idx,
		         count(*) OVER () AS total
		  FROM categories WHERE status = 'available'
		)
		INSERT INTO content_categories (content_id, category_id)
		SELECT c.id, cats.id
		FROM contents c JOIN cats ON cats.idx = (c.id % cats.total)
		WHERE c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.visibility = 'public'
		ON CONFLICT (content_id, category_id) DO NOTHING`); err != nil {
		return fmt.Errorf("seed: category links: %w", err)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (slug, name) VALUES
		  ('neon', '霓虹'), ('midnight', '深夜'), ('city', '城市'),
		  ('street', '街头'), ('live', '现场')
		ON CONFLICT (slug) DO NOTHING`); err != nil {
		return fmt.Errorf("seed: tags: %w", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO content_tags (content_id, tag_id)
		SELECT c.id, t.id FROM contents c
		JOIN tags t ON t.slug = CASE (c.id % 3)
		  WHEN 0 THEN 'neon' WHEN 1 THEN 'midnight' ELSE 'city' END
		WHERE c.publish_state = 'published' AND c.review_status = 'approved'
		  AND c.visibility = 'public'
		ON CONFLICT (content_id, tag_id) DO NOTHING`); err != nil {
		return fmt.Errorf("seed: content tags: %w", err)
	}

	// 真实播放事件（走 Redis 去重计数），让数据概览有可验证的有效播放量。
	counters := stats.NewCounters(cacheStore.Client())
	for i := int64(0); i < 3; i++ {
		_, _ = counters.RecordPlay(ctx, contentID,
			fmt.Sprintf("seed:%d", i), time.Now())
	}
	aggregator := stats.NewAggregator(db, cacheStore.Client(), log)
	if err := aggregator.RunOnce(ctx); err != nil {
		return fmt.Errorf("seed: aggregate: %w", err)
	}
	log.Info("seed complete", "admin_accounts", 4, "demo_content", slug,
		"note", "development data only")
	return nil
}
