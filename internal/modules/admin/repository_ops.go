package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/notifications"
)

// ── 用户管理（§4.7）───────────────────────────────────────────────

type AdminUserRow struct {
	ID           int64     `json:"id"`
	Handle       string    `json:"handle"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	LastSanction *string   `json:"last_sanction"`
	ContentCount int64     `json:"content_count"`
}

func (r *Repository) ListUsers(ctx context.Context, query string, limit int) ([]AdminUserRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.handle, u.role, u.status, u.created_at,
		       (SELECT action FROM user_sanctions s WHERE s.user_id = u.id
		        ORDER BY created_at DESC LIMIT 1),
		       (SELECT count(*) FROM contents c WHERE c.author_id = u.id)
		FROM users u
		WHERE ($1 = '' OR u.handle ILIKE '%' || $1 || '%')
		ORDER BY u.created_at DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: list users: %w", err)
	}
	defer rows.Close()
	var result []AdminUserRow
	for rows.Next() {
		var item AdminUserRow
		if err := rows.Scan(&item.ID, &item.Handle, &item.Role, &item.Status,
			&item.CreatedAt, &item.LastSanction, &item.ContentCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) SanctionUser(ctx context.Context, userID, adminID int64,
	action, reason string) (AdminUserRow, error) {
	if strings.TrimSpace(reason) == "" {
		return AdminUserRow{}, fmt.Errorf("%w: 处置理由必填", ErrInvalidInput)
	}
	newStatus := "active"
	switch action {
	case "warn":
		newStatus = "active"
	case "mute":
		newStatus = "muted"
	case "ban":
		newStatus = "banned"
	case "unban":
		newStatus = "active"
	case "verify_creator", "unverify_creator":
		newStatus = "active"
	default:
		return AdminUserRow{}, fmt.Errorf("%w: 未知处置动作", ErrInvalidInput)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return AdminUserRow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var handle, role string
	if err := tx.QueryRow(ctx, `
		SELECT handle, role FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&handle, &role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AdminUserRow{}, ErrNotFound
		}
		return AdminUserRow{}, err
	}
	// 认证与取消认证都改角色：创作者身份是角色位，不是状态位。
	// 「取消」是运营日常动作（资质过期、主动申请退出），所以必须有对称的下行路径，
	// 否则一旦授出就只能改库。
	switch action {
	case "verify_creator":
		if _, err := tx.Exec(ctx, `UPDATE users SET role = 'creator' WHERE id = $1`, userID); err != nil {
			return AdminUserRow{}, err
		}
		role = "creator"
	case "unverify_creator":
		if role == "admin" {
			return AdminUserRow{}, fmt.Errorf("%w: 管理员不能通过创作者认证撤回降级", ErrInvalidInput)
		}
		if role != "creator" {
			return AdminUserRow{}, fmt.Errorf("%w: 该用户不是创作者", ErrInvalidInput)
		}
		// 只退回到普通用户；已发布作品与粉丝关系都保留，不连带下架。
		if _, err := tx.Exec(ctx, `UPDATE users SET role = 'user' WHERE id = $1`, userID); err != nil {
			return AdminUserRow{}, err
		}
		role = "user"
	default:
		if _, err := tx.Exec(ctx, `UPDATE users SET status = $2 WHERE id = $1`,
			userID, newStatus); err != nil {
			return AdminUserRow{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_sanctions (user_id, admin_id, action, reason, notified)
		VALUES ($1, $2, $3, $4, true)`, userID, adminID, action, reason); err != nil {
		return AdminUserRow{}, err
	}
	title := map[string]string{"warn": "账号警告", "mute": "账号已被禁言",
		"ban": "账号已被封禁", "unban": "账号已恢复", "verify_creator": "创作者认证通过",
		"unverify_creator": "创作者认证已取消"}[action]
	if _, err := notifications.Dispatch(ctx, tx, notifications.Message{
		UserID:   userID,
		Kind:     "account",
		Category: notifications.KindSystem,
		Title:    title,
		Body:     reason,
	}); err != nil {
		return AdminUserRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminUserRow{}, err
	}
	return AdminUserRow{ID: userID, Handle: handle, Role: role, Status: newStatus}, nil
}

// ── 话题运营（§4.7）───────────────────────────────────────────────

type AdminTopic struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ContentCount int64  `json:"content_count"`
	Placement    string `json:"placement"`
	// Featured 单独标记「推荐」运营位：列表里的 placement 只展示最新一条，
	// 同时置顶时会显示成 pinned，按钮状态不能拿它判断。
	Featured  bool      `json:"featured"`
	CreatedAt time.Time `json:"created_at"`
}

func (r *Repository) ListTopics(ctx context.Context, status string) ([]AdminTopic, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.status,
		       (SELECT count(*) FROM content_topics ct WHERE ct.topic_id = t.id),
		       COALESCE((SELECT placement FROM topic_placements tp
		                 WHERE tp.topic_id = t.id ORDER BY tp.id DESC LIMIT 1), ''),
		       EXISTS (SELECT 1 FROM topic_placements tp
		               WHERE tp.topic_id = t.id AND tp.placement = 'featured'),
		       t.created_at
		FROM topics t
		WHERE ($1 = '' OR t.status = $1)
		ORDER BY t.created_at DESC LIMIT 100`, status)
	if err != nil {
		return nil, fmt.Errorf("admin: list topics: %w", err)
	}
	defer rows.Close()
	var result []AdminTopic
	for rows.Next() {
		var item AdminTopic
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Status,
			&item.ContentCount, &item.Placement, &item.Featured, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

var topicSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)

func (r *Repository) CreateTopic(ctx context.Context, slug, name string) (AdminTopic, error) {
	if !topicSlugPattern.MatchString(slug) || strings.TrimSpace(name) == "" {
		return AdminTopic{}, fmt.Errorf("%w: 话题 slug 或名称不合法", ErrInvalidInput)
	}
	var item AdminTopic
	err := r.db.QueryRow(ctx, `
		INSERT INTO topics (slug, name, status) VALUES ($1, $2, 'available')
		RETURNING id, slug, name, status, created_at`, slug, name).Scan(
		&item.ID, &item.Slug, &item.Name, &item.Status, &item.CreatedAt)
	if err != nil {
		return AdminTopic{}, fmt.Errorf("admin: create topic: %w", err)
	}
	return item, nil
}

func (r *Repository) SetTopicStatus(ctx context.Context, topicID int64, status string) (AdminTopic, error) {
	if status != "pending" && status != "available" && status != "offline" {
		return AdminTopic{}, fmt.Errorf("%w: 话题状态无效", ErrInvalidInput)
	}
	var item AdminTopic
	err := r.db.QueryRow(ctx, `
		UPDATE topics SET status = $2, updated_at = now() WHERE id = $1
		RETURNING id, slug, name, status, created_at`, topicID, status).Scan(
		&item.ID, &item.Slug, &item.Name, &item.Status, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminTopic{}, ErrNotFound
	}
	return item, err
}

// MergeTopics moves all content associations to the target and takes the source offline.
func (r *Repository) MergeTopics(ctx context.Context, sourceID, targetID int64) (int64, error) {
	if sourceID == targetID {
		return 0, fmt.Errorf("%w: 源话题与目标话题不能相同", ErrInvalidInput)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var moved int64
	if err := tx.QueryRow(ctx, `
		WITH moved AS (
		  INSERT INTO content_topics (content_id, topic_id)
		  SELECT content_id, $2 FROM content_topics WHERE topic_id = $1
		  ON CONFLICT (content_id, topic_id) DO NOTHING
		  RETURNING 1
		) SELECT count(*) FROM moved`, sourceID, targetID).Scan(&moved); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM content_topics WHERE topic_id = $1`, sourceID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE topics SET status = 'offline', updated_at = now() WHERE id = $1`, sourceID); err != nil {
		return 0, err
	}
	return moved, tx.Commit(ctx)
}

func (r *Repository) SetTopicPlacement(ctx context.Context, topicID, adminID int64,
	placement string, effectiveAt time.Time, enabled bool) error {
	if placement != "featured" && placement != "pinned" {
		return fmt.Errorf("%w: 运营位类型无效", ErrInvalidInput)
	}
	// 取消推荐 / 置顶：删掉这一条运营位记录，而不是插入一条空值。
	if !enabled {
		_, err := r.db.Exec(ctx, `
			DELETE FROM topic_placements WHERE topic_id = $1 AND placement = $2`,
			topicID, placement)
		return err
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO topic_placements (topic_id, placement, effective_at, created_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (topic_id, placement) DO UPDATE
		  SET effective_at = EXCLUDED.effective_at, created_by = EXCLUDED.created_by`,
		topicID, placement, effectiveAt, adminID)
	return err
}

// ── 统计读取（§7.6，概览只读聚合表）──────────────────────────────

type Delta struct {
	Value     float64 `json:"value"`
	Direction string  `json:"direction"` // up / down / flat
	Label     string  `json:"label"`     // 带箭头与正负号，不只靠颜色
}

type Overview struct {
	DataReady            bool             `json:"data_ready"`
	PeriodLabel          string           `json:"period_label"`
	UpdatedAt            *time.Time       `json:"updated_at"`
	TotalPlays           int64            `json:"total_plays"`
	NewUsers             int64            `json:"new_users"`
	PendingReviews       int64            `json:"pending_reviews"`
	ReportResolutionRate float64          `json:"report_resolution_rate"`
	AvgWaitMinutes       float64          `json:"avg_wait_minutes"`
	Deltas               map[string]Delta `json:"deltas"`
	Note                 string           `json:"note"`
}

type TrendPoint struct {
	Day      string `json:"day"`
	Plays    int64  `json:"plays"`
	NewUsers int64  `json:"new_users"`
}

type HotItem struct {
	Rank      int    `json:"rank"`
	ContentID int64  `json:"content_id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Handle    string `json:"handle"`
	Plays     int64  `json:"plays"`
	Likes     int64  `json:"likes"`
	Status    string `json:"status"`
}

func makeDelta(current, previous float64) Delta {
	direction := "flat"
	if previous == 0 {
		if current > 0 {
			direction = "up"
		}
		return Delta{Value: 0, Direction: direction, Label: "—"}
	}
	value := (current - previous) / previous * 100
	if value > 0.05 {
		direction = "up"
	} else if value < -0.05 {
		direction = "down"
	}
	arrow := map[string]string{"up": "↑", "down": "↓", "flat": "→"}[direction]
	return Delta{Value: value, Direction: direction,
		Label: fmt.Sprintf("%s %+.1f%%", arrow, value)}
}

func (r *Repository) Overview(ctx context.Context) (Overview, error) {
	result := Overview{PeriodLabel: "近 7 日 · 每 5 分钟更新", Deltas: map[string]Delta{}}
	var latestBucket time.Time
	err := r.db.QueryRow(ctx, `
		SELECT bucket, total_plays, pending_reviews, created_at
		FROM stats_overview_5m ORDER BY bucket DESC LIMIT 1`).Scan(
		&latestBucket, &result.TotalPlays, &result.PendingReviews, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		result.Note = "统计聚合尚未运行，暂不展示指标（避免 0 值误导）"
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("admin: overview: %w", err)
	}
	result.DataReady = true

	var plays7, users7, reports7, resolved7 int64
	if err := r.db.QueryRow(ctx, `
		SELECT COALESCE(sum(plays),0), COALESCE(sum(new_users),0),
		       COALESCE(sum(new_reports),0), COALESCE(sum(resolved_reports),0)
		FROM stats_daily WHERE day > current_date - interval '7 days'`).Scan(
		&plays7, &users7, &reports7, &resolved7); err != nil {
		return result, err
	}
	result.NewUsers = users7
	if reports7 > 0 {
		result.ReportResolutionRate = float64(resolved7) / float64(reports7) * 100
	}

	var playsPrev, usersPrev, reportsPrev, resolvedPrev int64
	if err := r.db.QueryRow(ctx, `
		SELECT COALESCE(sum(plays),0), COALESCE(sum(new_users),0),
		       COALESCE(sum(new_reports),0), COALESCE(sum(resolved_reports),0)
		FROM stats_daily
		WHERE day <= current_date - interval '7 days'
		  AND day > current_date - interval '14 days'`).Scan(
		&playsPrev, &usersPrev, &reportsPrev, &resolvedPrev); err != nil {
		return result, err
	}
	result.Deltas["plays"] = makeDelta(float64(plays7), float64(playsPrev))
	result.Deltas["new_users"] = makeDelta(float64(users7), float64(usersPrev))
	prevRate := 0.0
	if reportsPrev > 0 {
		prevRate = float64(resolvedPrev) / float64(reportsPrev) * 100
	}
	result.Deltas["report_resolution_rate"] = makeDelta(result.ReportResolutionRate, prevRate)
	result.Deltas["pending_reviews"] = makeDelta(float64(result.PendingReviews),
		float64(result.PendingReviews))

	// 平均等待时长：近 7 日首次审核决定 - 提交时间（§7.6）。
	_ = r.db.QueryRow(ctx, `
		SELECT COALESCE(avg(EXTRACT(EPOCH FROM (d.created_at - COALESCE(c.published_at, c.created_at))) / 60), 0)
		FROM content_review_decisions d
		JOIN contents c ON c.id = d.content_id
		WHERE d.id IN (SELECT min(id) FROM content_review_decisions GROUP BY content_id)
		  AND d.created_at > now() - interval '7 days'`).Scan(&result.AvgWaitMinutes)
	result.Note = "播放量按 30 分钟同用户同内容去重；聚合失败保留上一周期数据"
	return result, nil
}

func (r *Repository) Trend(ctx context.Context, days int) ([]TrendPoint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT to_char(day, 'YYYY-MM-DD'), plays, new_users
		FROM stats_daily WHERE day > current_date - $1::int
		ORDER BY day ASC`, days)
	if err != nil {
		return nil, fmt.Errorf("admin: trend: %w", err)
	}
	defer rows.Close()
	var result []TrendPoint
	for rows.Next() {
		var point TrendPoint
		if err := rows.Scan(&point.Day, &point.Plays, &point.NewUsers); err != nil {
			return nil, err
		}
		result = append(result, point)
	}
	return result, rows.Err()
}

func (r *Repository) Hot(ctx context.Context, limit int) ([]HotItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT h.rank_no, c.id, c.slug, c.title, u.handle, h.plays, h.likes,
		       CASE WHEN c.review_status = 'approved' THEN 'approved' ELSE 'reviewing' END
		FROM hot_contents h
		JOIN contents c ON c.id = h.content_id
		JOIN users u ON u.id = c.author_id
		ORDER BY h.rank_no ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: hot: %w", err)
	}
	defer rows.Close()
	var result []HotItem
	for rows.Next() {
		var item HotItem
		if err := rows.Scan(&item.Rank, &item.ContentID, &item.Slug, &item.Title,
			&item.Handle, &item.Plays, &item.Likes, &item.Status); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ── 混合来源（§4.5 / §4.6）───────────────────────────────────────

type Connector struct {
	ID      int64           `json:"id"`
	Slug    string          `json:"slug"`
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	BaseURL string          `json:"base_url"`
	Status  string          `json:"status"`
	Config  json.RawMessage `json:"config"`
	// 密钥引用名只在服务端保存，前端只看到「已配置 / 未配置」（§4.7）。
	SecretRef        string     `json:"-"`
	SecretConfigured bool       `json:"secret_configured"`
	AllowDirectPlay  bool       `json:"allow_direct_play"`
	AllowImport      bool       `json:"allow_import"`
	LastRefreshAt    *time.Time `json:"last_refresh_at"`
	LastError        string     `json:"last_error"`
	CreatedAt        time.Time  `json:"created_at"`
}

type ConnectorInput struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	BaseURL         string `json:"base_url"`
	SecretRef       string `json:"secret_ref"`
	AllowDirectPlay bool   `json:"allow_direct_play"`
	AllowImport     bool   `json:"allow_import"`
	Status          string `json:"status"`
}

func (r *Repository) ListConnectors(ctx context.Context) ([]Connector, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, slug, name, kind, base_url, status, config::text, secret_ref,
		       allow_direct_play, allow_import, last_refresh_at, last_error, created_at
		FROM source_connectors ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("admin: connectors: %w", err)
	}
	defer rows.Close()
	var result []Connector
	for rows.Next() {
		var item Connector
		var config string
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Kind, &item.BaseURL,
			&item.Status, &config, &item.SecretRef, &item.AllowDirectPlay,
			&item.AllowImport, &item.LastRefreshAt, &item.LastError, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Config = json.RawMessage(config)
		item.SecretConfigured = item.SecretRef != ""
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) UpsertConnector(ctx context.Context, in ConnectorInput) (Connector, error) {
	if !topicSlugPattern.MatchString(in.Slug) || strings.TrimSpace(in.Name) == "" {
		return Connector{}, fmt.Errorf("%w: Connector slug 或名称不合法", ErrInvalidInput)
	}
	if in.Kind != "webdav" && in.Kind != "third_party_api" {
		return Connector{}, fmt.Errorf("%w: Connector 类型无效", ErrInvalidInput)
	}
	status := in.Status
	if status == "" {
		status = "unauthorized"
	}
	if status != "active" && status != "disabled" && status != "unauthorized" && status != "error" {
		return Connector{}, fmt.Errorf("%w: Connector 状态无效", ErrInvalidInput)
	}
	var id int64
	err := r.db.QueryRow(ctx, `
		INSERT INTO source_connectors
		  (slug, name, kind, base_url, status, config, secret_ref, allow_direct_play, allow_import)
		VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, $6, $7, $8)
		ON CONFLICT (slug) DO UPDATE SET
		  name = EXCLUDED.name, kind = EXCLUDED.kind, base_url = EXCLUDED.base_url,
		  status = EXCLUDED.status, secret_ref = EXCLUDED.secret_ref,
		  allow_direct_play = EXCLUDED.allow_direct_play,
		  allow_import = EXCLUDED.allow_import, updated_at = now()
		RETURNING id`, in.Slug, in.Name, in.Kind, in.BaseURL, status,
		in.SecretRef, in.AllowDirectPlay, in.AllowImport).Scan(&id)
	if err != nil {
		return Connector{}, fmt.Errorf("admin: upsert connector: %w", err)
	}
	list, err := r.ListConnectors(ctx)
	if err != nil {
		return Connector{}, err
	}
	for _, item := range list {
		if item.ID == id {
			return item, nil
		}
	}
	return Connector{}, ErrNotFound
}

func (r *Repository) RefreshConnector(ctx context.Context, id int64) (Connector, error) {
	var connector Connector
	err := r.db.QueryRow(ctx, `
		UPDATE source_connectors SET last_refresh_at = now(), last_error = '', updated_at = now()
		WHERE id = $1 RETURNING id, slug, name, kind, base_url, status`, id).Scan(
		&connector.ID, &connector.Slug, &connector.Name, &connector.Kind,
		&connector.BaseURL, &connector.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connector{}, ErrNotFound
	}
	return connector, err
}

type ImportJob struct {
	ID          int64     `json:"id"`
	ConnectorID int64     `json:"connector_id"`
	ExternalID  string    `json:"external_id"`
	SourceURL   string    `json:"source_url"`
	Status      string    `json:"status"`
	ContentID   *int64    `json:"content_id"`
	ByteSize    int64     `json:"byte_size"`
	LastError   string    `json:"last_error"`
	CreatedAt   time.Time `json:"created_at"`
}

func (r *Repository) ListImports(ctx context.Context, limit int) ([]ImportJob, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, connector_id, external_id, source_url, status, content_id,
		       byte_size, last_error, created_at
		FROM import_jobs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: imports: %w", err)
	}
	defer rows.Close()
	var result []ImportJob
	for rows.Next() {
		var item ImportJob
		if err := rows.Scan(&item.ID, &item.ConnectorID, &item.ExternalID,
			&item.SourceURL, &item.Status, &item.ContentID, &item.ByteSize,
			&item.LastError, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type ImportInput struct {
	ConnectorID    int64  `json:"connector_id"`
	ExternalID     string `json:"external_id"`
	SourceURL      string `json:"source_url"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (r *Repository) CreateImport(ctx context.Context, in ImportInput) (ImportJob, error) {
	var item ImportJob
	err := r.db.QueryRow(ctx, `
		INSERT INTO import_jobs (connector_id, external_id, source_url, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING id, connector_id, external_id, source_url, status, byte_size, last_error, created_at`,
		in.ConnectorID, in.ExternalID, in.SourceURL, in.IdempotencyKey).Scan(
		&item.ID, &item.ConnectorID, &item.ExternalID, &item.SourceURL,
		&item.Status, &item.ByteSize, &item.LastError, &item.CreatedAt)
	if err != nil {
		return ImportJob{}, fmt.Errorf("admin: create import: %w", err)
	}
	return item, nil
}

// RetryDeadLetters resets failed delivery jobs so the Worker re-runs them
// (开发技术规范 §10.7「任务重试」)。只重置状态，不修改业务数据。
func (r *Repository) RetryDeadLetters(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'pending', attempt_count = 0, last_error = NULL,
		    scheduled_at = now(), lease_owner = NULL, lease_expires_at = NULL,
		    started_at = NULL, updated_at = now()
		WHERE status IN ('dead_letter', 'failed')`)
	if err != nil {
		return 0, fmt.Errorf("admin: retry delivery jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── Outbox 事件可观测与重放（§10.4 / §10.7）──────────────────────
//
// Outbox 是写路径与交付调度之间唯一的纽带：事件失败意味着「内容已变更但页面
// 没有失效」。失败事件必须可见、可重放，否则生产里只能靠手工 SQL 自愈。

type AdminOutboxEvent struct {
	ID            int64      `json:"id"`
	EventKey      string     `json:"event_key"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	ContentID     int64      `json:"content_id,omitempty"`
	Slug          string     `json:"slug,omitempty"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	CreatedAt     time.Time  `json:"created_at"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
}

// ListOutboxEvents 按状态列出事件；status 为空表示全部。
func (r *Repository) ListOutboxEvents(ctx context.Context, status string, limit int) ([]AdminOutboxEvent, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, event_key, event_type, status, attempts, COALESCE(last_error, ''),
		       CASE WHEN payload->>'content_id' ~ '^[0-9]+$'
		            THEN (payload->>'content_id')::bigint ELSE 0 END,
		       COALESCE(payload->>'slug', ''),
		       next_attempt_at, created_at, processed_at
		FROM outbox_events
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: list outbox events: %w", err)
	}
	defer rows.Close()
	items := make([]AdminOutboxEvent, 0, limit)
	for rows.Next() {
		var item AdminOutboxEvent
		if err := rows.Scan(&item.ID, &item.EventKey, &item.EventType, &item.Status,
			&item.Attempts, &item.LastError, &item.ContentID, &item.Slug,
			&item.NextAttemptAt, &item.CreatedAt, &item.ProcessedAt); err != nil {
			return nil, fmt.Errorf("admin: scan outbox event: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// RetryOutboxEvents 把失败事件重置回 pending；ids 为空时重放全部失败事件。
// 只重排状态，不改业务数据；重放后仍失败（如内容已硬删）会再次进入 failed。
func (r *Repository) RetryOutboxEvents(ctx context.Context, ids []int64) (int64, error) {
	query := `UPDATE outbox_events
		SET status = 'pending', attempts = 0, last_error = NULL,
		    next_attempt_at = now(), processed_at = NULL
		WHERE status = 'failed'`
	var args []any
	if len(ids) > 0 {
		query += ` AND id = ANY($1)`
		args = append(args, ids)
	}
	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("admin: retry outbox events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── 系统设置与 Delivery 诊断（§4.7 / §10.7）──────────────────────

func (r *Repository) Diagnostics(ctx context.Context) (map[string]any, error) {
	result := map[string]any{}
	var connectors, pendingImports, pendingReviews, reports int64
	if err := r.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM source_connectors),
		       (SELECT count(*) FROM import_jobs WHERE status IN ('pending','downloading','transcoding')),
		       (SELECT count(*) FROM contents WHERE review_status = 'pending' AND publish_state = 'published'),
		       (SELECT count(*) FROM reports WHERE status = 'pending')`).Scan(
		&connectors, &pendingImports, &pendingReviews, &reports); err != nil {
		return nil, err
	}
	result["connectors"], result["pending_imports"] = connectors, pendingImports
	result["pending_reviews"], result["pending_reports"] = pendingReviews, reports
	rows, err := r.db.Query(ctx, `
		SELECT status, count(*) FROM delivery_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		jobs[status] = count
	}
	result["delivery_jobs"] = jobs

	// Outbox 状态分布：failed 表示需要人工重放的事件（§10.4 终态）。
	outboxRows, err := r.db.Query(ctx, `
		SELECT status, count(*) FROM outbox_events GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer outboxRows.Close()
	outbox := map[string]int64{}
	for outboxRows.Next() {
		var status string
		var count int64
		if err := outboxRows.Scan(&status, &count); err != nil {
			return nil, err
		}
		outbox[status] = count
	}
	if err := outboxRows.Err(); err != nil {
		return nil, err
	}
	result["outbox_events"] = outbox

	var stalePages int64
	_ = r.db.QueryRow(ctx, `
		SELECT count(*) FROM delivery_pages
		WHERE desired_version > published_version`).Scan(&stalePages)
	result["stale_pages"] = stalePages
	return result, nil
}
