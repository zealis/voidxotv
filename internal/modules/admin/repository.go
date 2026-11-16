package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yejie/voidxotv/internal/modules/notifications"
)

var (
	ErrNotFound     = errors.New("admin: object not found")
	ErrConflict     = errors.New("admin: state conflict")
	ErrInvalidInput = errors.New("admin: invalid input")
)

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// ── 审计与账号 ────────────────────────────────────────────────────

func (r *Repository) WriteAudit(ctx context.Context, adminID int64, role, action,
	objectType, objectID string, before, after, ip any, userAgent string) error {
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	_, err := r.db.Exec(ctx, `
		INSERT INTO admin_audit_logs
		  (admin_id, role, action, object_type, object_id, before_summary, after_summary,
		   ip_address, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::inet, now())`,
		adminID, role, action, objectType, objectID,
		string(beforeJSON), string(afterJSON), ip)
	if err != nil {
		return fmt.Errorf("admin: write audit: %w", err)
	}
	_ = userAgent // 审计表当前不含 UA；保留参数以便迁移后扩展，绝不写入日志正文。
	return nil
}

type AuditEntry struct {
	ID         int64           `json:"id"`
	AdminID    int64           `json:"admin_id"`
	AdminEmail string          `json:"admin_email"`
	Role       string          `json:"role"`
	Action     string          `json:"action"`
	ObjectType string          `json:"object_type"`
	ObjectID   string          `json:"object_id"`
	Before     json.RawMessage `json:"before_summary"`
	After      json.RawMessage `json:"after_summary"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ListAudit 按 id 倒序的游标分页；actionPrefix 非空时按动作前缀过滤
// （如 "review." / "user." / "settings."），LIKE 通配符转义防注入。
func (r *Repository) ListAudit(ctx context.Context, limit, cursor int64, actionPrefix string) ([]AuditEntry, error) {
	pattern := ""
	if actionPrefix != "" {
		// LIKE 通配符转义：把 % _ \ 都按字面量处理，防前缀注入通配符。
		replacer := strings.NewReplacer(
			`\`, `\\`,
			`%`, `\%`,
			`_`, `\_`,
		)
		pattern = "%" + replacer.Replace(actionPrefix) + "%"
	}
	rows, err := r.db.Query(ctx, `
		SELECT a.id, a.admin_id, u.email, a.role, a.action, a.object_type, a.object_id,
		       COALESCE(a.before_summary::text, 'null'), COALESCE(a.after_summary::text, 'null'),
		       a.created_at
		FROM admin_audit_logs a JOIN admin_users u ON u.id = a.admin_id
		WHERE ($1 = 0 OR a.id < $1)
		  AND ($2 = '' OR a.action LIKE $2)
		ORDER BY a.id DESC LIMIT $3`, cursor, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: list audit: %w", err)
	}
	defer rows.Close()
	var result []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		var before, after string
		if err := rows.Scan(&entry.ID, &entry.AdminID, &entry.AdminEmail, &entry.Role,
			&entry.Action, &entry.ObjectType, &entry.ObjectID, &before, &after,
			&entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.Before, entry.After = json.RawMessage(before), json.RawMessage(after)
		result = append(result, entry)
	}
	return result, rows.Err()
}

type AdminAccount struct {
	ID           int64
	Email        string
	PasswordHash string
	Role         string
	Status       string
	// TOTPSecret 非空即表示该后台账号已启用二次验证（§11.6）。
	TOTPSecret string
}

// FindActiveAdmin 按邮箱查后台账号。调用方先过 auth.NormalizeEmail，
// 这里再 lower() 兜底：唯一索引建在 lower(email) 上，大小写不会漏查。
func (r *Repository) FindActiveAdmin(ctx context.Context, email string) (AdminAccount, error) {
	var account AdminAccount
	err := r.db.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status, COALESCE(totp_secret, '')
		FROM admin_users WHERE lower(email) = lower($1)`, email).Scan(
		&account.ID, &account.Email, &account.PasswordHash, &account.Role,
		&account.Status, &account.TOTPSecret)
	if errors.Is(err, pgx.ErrNoRows) {
		return account, ErrBadLogin
	}
	if err != nil {
		return account, fmt.Errorf("admin: find account: %w", err)
	}
	if account.Status != "active" {
		return account, ErrBadLogin
	}
	return account, nil
}

// ── 审核队列（§9.4）───────────────────────────────────────────────

type ReviewItem struct {
	ContentID      int64    `json:"content_id"`
	Slug           string   `json:"slug"`
	Type           string   `json:"type"`
	Title          string   `json:"title"`
	AuthorID       int64    `json:"author_id"`
	Handle         string   `json:"handle"`
	PublishedAt    string   `json:"published_at"`
	ReviewStatus   string   `json:"review_status"`
	RiskScore      int      `json:"risk_score"`
	ReportCount    int      `json:"report_count"`
	RiskLabels     []string `json:"risk_labels"`
	WaitingMinutes int64    `json:"waiting_minutes"`
	PosterKey      string   `json:"-"`
	PosterURL      string   `json:"poster_url"`
}

const reviewQueueSQL = `
	SELECT c.id, c.slug, c.type, c.title, c.author_id, u.handle,
	       to_char(COALESCE(c.published_at, c.created_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       c.review_status,
	       COALESCE(rp.reports, 0)::int,
	       COALESCE(sg.machine, 0)::int,
	       COALESCE(sg.copyright, 0)::int,
	       (EXTRACT(EPOCH FROM (now() - COALESCE(c.published_at, c.created_at))) / 60)::bigint,
	       COALESCE((
	         SELECT s.object_key FROM media_assets m
	         JOIN storage_objects s ON s.media_asset_id = m.id
	         WHERE m.content_id = c.id AND m.kind IN ('cover', 'image')
	           AND s.driver = 's3'
	         ORDER BY m.position, m.id, CASE s.role WHEN 'display' THEN 0 ELSE 1 END LIMIT 1
	       ), '')
	FROM contents c
	JOIN users u ON u.id = c.author_id
	LEFT JOIN (
	  SELECT object_id, count(*)::int AS reports FROM reports
	  WHERE object_type = 'content' AND status = 'pending'
	  GROUP BY object_id
	) rp ON rp.object_id = c.id
	LEFT JOIN (
	  SELECT content_id,
	         sum(CASE WHEN signal = 'machine' THEN level ELSE 0 END)::int AS machine,
	         sum(CASE WHEN signal = 'copyright' THEN level ELSE 0 END)::int AS copyright
	  FROM content_risk_signals GROUP BY content_id
	) sg ON sg.content_id = c.id
	WHERE c.publish_state = 'published'
	  AND ((c.review_status IN ('pending', 'limited'))
	       OR (c.review_status = 'approved'
	           AND (COALESCE(rp.reports, 0) > 0 OR COALESCE(sg.machine, 0) > 0
	                OR COALESCE(sg.copyright, 0) > 0)))
`

func (r *Repository) ReviewQueue(ctx context.Context, limit int) ([]ReviewItem, error) {
	rows, err := r.db.Query(ctx, reviewQueueSQL+`
	  ORDER BY (COALESCE(rp.reports, 0) >= 3) DESC,
	           (COALESCE(rp.reports, 0) * 10 + COALESCE(sg.machine, 0) * 5
	            + COALESCE(sg.copyright, 0) * 20) DESC,
	           COALESCE(c.published_at, c.created_at) ASC
	  LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: review queue: %w", err)
	}
	defer rows.Close()
	return scanReviewItems(rows)
}

func scanReviewItems(rows pgx.Rows) ([]ReviewItem, error) {
	var result []ReviewItem
	for rows.Next() {
		var item ReviewItem
		var reports, machine, copyright int
		if err := rows.Scan(&item.ContentID, &item.Slug, &item.Type, &item.Title,
			&item.AuthorID, &item.Handle, &item.PublishedAt, &item.ReviewStatus,
			&reports, &machine, &copyright, &item.WaitingMinutes, &item.PosterKey); err != nil {
			return nil, fmt.Errorf("admin: scan review: %w", err)
		}
		item.ReportCount = reports
		item.RiskScore = reports*10 + machine*5 + copyright*20
		item.RiskLabels = []string{}
		if reports > 0 {
			item.RiskLabels = append(item.RiskLabels, fmt.Sprintf("举报 %d 次", reports))
		}
		if machine > 0 {
			item.RiskLabels = append(item.RiskLabels, "疑似机器检测信号")
		}
		if copyright > 0 {
			item.RiskLabels = append(item.RiskLabels, "版权争议")
		}
		if reports >= 3 {
			item.RiskLabels = append(item.RiskLabels, "高风险置顶")
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) ReviewItem(ctx context.Context, contentID int64) (ReviewItem, error) {
	rows, err := r.db.Query(ctx, reviewQueueSQL+` AND c.id = $1`, contentID)
	if err != nil {
		return ReviewItem{}, fmt.Errorf("admin: review item: %w", err)
	}
	defer rows.Close()
	items, err := scanReviewItems(rows)
	if err != nil {
		return ReviewItem{}, err
	}
	if len(items) == 0 {
		return ReviewItem{}, ErrNotFound
	}
	return items[0], nil
}

type DecisionResult struct {
	ContentID     int64  `json:"content_id"`
	Slug          string `json:"slug"`
	Action        string `json:"action"`
	ReviewStatus  string `json:"review_status"`
	FeedEligible  bool   `json:"feed_eligible"`
	Recommendable bool   `json:"recommend_eligible"`
	DecisionID    int64  `json:"decision_id"`
}

// applyDecisionTx is shared by direct moderation and report-linked takedowns.
// It updates the content state, records the decision, emits a transactional
// Outbox event (so Delivery invalidates the detail page) and notifies the author.
func applyDecisionTx(ctx context.Context, tx pgx.Tx, contentID, adminID int64,
	action, reason string) (DecisionResult, error) {

	var result DecisionResult
	var state, visibility, slug string
	var authorID int64
	err := tx.QueryRow(ctx, `
		SELECT publish_state, review_status, visibility, slug, author_id
		FROM contents WHERE id = $1 FOR UPDATE`, contentID).Scan(
		&state, &result.ReviewStatus, &visibility, &slug, &authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if state != "published" {
		return result, fmt.Errorf("%w: 仅已发布内容可审核", ErrConflict)
	}
	if (action == "reject" || action == "take_down") && strings.TrimSpace(reason) == "" {
		return result, fmt.Errorf("%w: 驳回与下架必须填写理由", ErrInvalidInput)
	}

	newStatus := result.ReviewStatus
	feedEligible := true
	recommendable := true
	switch action {
	case "approve":
		newStatus, feedEligible, recommendable = "approved", visibility == "public", true
	case "reject":
		newStatus, feedEligible, recommendable = "rejected", false, false
	case "take_down":
		newStatus, feedEligible, recommendable = "taken_down", false, false
	case "limit":
		// 保留落地页（仍 approved / feed_eligible），只退出推荐与热门。
		newStatus, feedEligible, recommendable = "approved", true, false
	case "restore":
		newStatus, feedEligible, recommendable = "approved", visibility == "public", true
	default:
		return result, fmt.Errorf("%w: 未知审核动作 %q", ErrInvalidInput, action)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE contents
		SET review_status = $2, feed_eligible = $3, recommend_eligible = $4, updated_at = now()
		WHERE id = $1`, contentID, newStatus, feedEligible, recommendable); err != nil {
		return result, err
	}
	var decisionID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO content_review_decisions (content_id, admin_id, action, reason, risk_score)
		VALUES ($1, $2, $3, $4, 0) RETURNING id`,
		contentID, adminID, action, reason).Scan(&decisionID); err != nil {
		return result, err
	}
	// 审核变更必须让 Delivery 精确失效该落地页与 sitemap（§9.4 / §10）。
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_key, event_type, payload)
		VALUES ($1, 'content.review_changed',
		  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text, 'action', $4::text))
		ON CONFLICT (event_key) DO NOTHING`,
		fmt.Sprintf("content.review_changed:%d:%d", contentID, decisionID),
		contentID, slug, action); err != nil {
		return result, err
	}
	if action == "reject" || action == "take_down" {
		title := "作品未通过审核"
		if action == "take_down" {
			title = "作品已下架"
		}
		// 统一走 Dispatch：审核结论属于「系统通知」，受设置页开关约束（§9.1）。
		if _, err := notifications.Dispatch(ctx, tx, notifications.Message{
			UserID:   authorID,
			Kind:     "moderation",
			Category: notifications.KindSystem,
			Title:    title,
			Body:     fmt.Sprintf("%s（%s）：%s。如有异议可申诉。", title, slug, reason),
		}); err != nil {
			return result, err
		}
	}
	result.ContentID, result.Slug = contentID, slug
	result.Action, result.ReviewStatus = action, newStatus
	result.FeedEligible, result.Recommendable = feedEligible, recommendable
	result.DecisionID = decisionID
	return result, nil
}

func (r *Repository) ApplyDecision(ctx context.Context, contentID, adminID int64,
	action, reason string) (DecisionResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return DecisionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := applyDecisionTx(ctx, tx, contentID, adminID, action, reason)
	if err != nil {
		return DecisionResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (r *Repository) BatchApprove(ctx context.Context, ids []int64, adminID int64) (int, error) {
	approved := 0
	for _, id := range ids {
		if _, err := r.ApplyDecision(ctx, id, adminID, "approve", ""); err == nil {
			approved++
		}
	}
	return approved, nil
}

// ── 举报（§9.5）───────────────────────────────────────────────────

type ReportCase struct {
	ID          int64  `json:"id"`
	ObjectType  string `json:"object_type"`
	ObjectID    int64  `json:"object_id"`
	ObjectTitle string `json:"object_title"`
	// ObjectPath 是可跳转的作品深链接（评论举报给所属作品，用户举报为空）。
	ObjectPath   string     `json:"object_path"`
	Status       string     `json:"status"`
	ReportCount  int        `json:"report_count"`
	TopReason    string     `json:"top_reason"`
	Resolution   string     `json:"resolution"`
	LinkedAction string     `json:"linked_action"`
	OpenedAt     time.Time  `json:"opened_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
}

func (r *Repository) ListReportCases(ctx context.Context, limit int) ([]ReportCase, error) {
	rows, err := r.db.Query(ctx, `
		SELECT rc.id, rc.object_type, rc.object_id,
		       CASE
		         WHEN rc.object_type = 'content' THEN COALESCE(
		           (SELECT title FROM contents WHERE id = rc.object_id), '(作品已删除)')
		         WHEN rc.object_type = 'comment' THEN COALESCE(
		           (SELECT left(regexp_replace(btrim(cm.body),
		             '\[\[gif:[^\]]*\]\]', '[动图]', 'g'), 60) FROM comments cm
		            WHERE cm.id = rc.object_id), '(评论已删除)')
		         WHEN rc.object_type = 'user' THEN COALESCE(
		           (SELECT COALESCE(NULLIF(btrim(u.display_name), ''), u.email)
		            FROM users u WHERE u.id = rc.object_id), '(用户已删除)')
		         ELSE '(对象已删除)'
		       END,
		       COALESCE((
		         SELECT CASE WHEN c.type = 'image' THEN '/image/' ELSE '/video/' END || c.slug
		         FROM contents c
		         WHERE c.id = CASE
		           WHEN rc.object_type = 'content' THEN rc.object_id
		           WHEN rc.object_type = 'comment' THEN
		             (SELECT cm.content_id FROM comments cm WHERE cm.id = rc.object_id)
		           ELSE NULL END
		       ), ''),
		       rc.status, rc.report_count,
		       COALESCE((SELECT reason FROM reports WHERE case_id = rc.id
		                 ORDER BY created_at ASC LIMIT 1), 'other'),
		       rc.resolution, rc.linked_action, rc.opened_at, rc.resolved_at
		FROM report_cases rc
		ORDER BY (rc.status = 'pending') DESC, rc.report_count DESC, rc.opened_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: report cases: %w", err)
	}
	defer rows.Close()
	var result []ReportCase
	for rows.Next() {
		var item ReportCase
		if err := rows.Scan(&item.ID, &item.ObjectType, &item.ObjectID, &item.ObjectTitle,
			&item.ObjectPath, &item.Status, &item.ReportCount, &item.TopReason, &item.Resolution,
			&item.LinkedAction, &item.OpenedAt, &item.ResolvedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) ResolveReportCase(ctx context.Context, caseID, adminID int64,
	outcome, linkedAction, resolution string) (ReportCase, error) {
	if outcome != "upheld" && outcome != "dismissed" {
		return ReportCase{}, fmt.Errorf("%w: 处置结论无效", ErrInvalidInput)
	}
	if strings.TrimSpace(resolution) == "" {
		return ReportCase{}, fmt.Errorf("%w: 处置说明必填", ErrInvalidInput)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return ReportCase{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var objectType string
	var objectID int64
	var status string
	err = tx.QueryRow(ctx, `
		SELECT object_type, object_id, status FROM report_cases
		WHERE id = $1 FOR UPDATE`, caseID).Scan(&objectType, &objectID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportCase{}, ErrNotFound
	}
	if err != nil {
		return ReportCase{}, err
	}
	if status != "pending" {
		return ReportCase{}, fmt.Errorf("%w: 举报单已处置", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE report_cases SET status = $2, resolution = $3, linked_action = $4,
		  resolved_by = $5, resolved_at = now() WHERE id = $1`,
		caseID, outcome, resolution, linkedAction, adminID); err != nil {
		return ReportCase{}, err
	}
	reportStatus := "dismissed"
	if outcome == "upheld" {
		reportStatus = "upheld"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reports SET status = $2, resolved_at = now() WHERE case_id = $1`,
		caseID, reportStatus); err != nil {
		return ReportCase{}, err
	}
	// 处置联动：成立时可下架内容 / 警告或封禁用户，分别写审计（handler 层）。
	if outcome == "upheld" && objectType == "content" {
		switch linkedAction {
		case "take_down":
			if _, err := applyDecisionTx(ctx, tx, objectID, adminID, "take_down", resolution); err != nil {
				return ReportCase{}, err
			}
		case "limit":
			if _, err := applyDecisionTx(ctx, tx, objectID, adminID, "limit", ""); err != nil {
				return ReportCase{}, err
			}
		}
	}
	// 评论举报成立：按联动选项软删除评论，口径与前台删除评论一致。
	if outcome == "upheld" && objectType == "comment" && linkedAction == "remove_comment" {
		if _, err := tx.Exec(ctx, `
			UPDATE comments SET status = 'removed' WHERE id = $1 AND status <> 'removed'`,
			objectID); err != nil {
			return ReportCase{}, err
		}
	}
	// 向举报人反馈结果（§9.5）。逐个举报人走 Dispatch，
	// 这样「系统通知」开关对举报反馈同样生效，而不是绕过偏好直写。
	rows, err := tx.Query(ctx, `SELECT DISTINCT reporter_id FROM reports WHERE case_id = $1`, caseID)
	if err != nil {
		return ReportCase{}, err
	}
	var reporters []int64
	for rows.Next() {
		var reporterID int64
		if err := rows.Scan(&reporterID); err != nil {
			rows.Close()
			return ReportCase{}, err
		}
		reporters = append(reporters, reporterID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReportCase{}, err
	}
	for _, reporterID := range reporters {
		if _, err := notifications.Dispatch(ctx, tx, notifications.Message{
			UserID:   reporterID,
			Kind:     "report",
			Category: notifications.KindSystem,
			Title:    "举报处理结果",
			Body:     fmt.Sprintf("你提交的举报已处理：%s。%s", outcomeText(outcome), resolution),
		}); err != nil {
			return ReportCase{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ReportCase{}, err
	}
	return ReportCase{ID: caseID, ObjectType: objectType, ObjectID: objectID,
		Status: outcome, Resolution: resolution, LinkedAction: linkedAction}, nil
}

func outcomeText(outcome string) string {
	if outcome == "upheld" {
		return "举报成立，已按规则处置"
	}
	return "举报不成立，未作处置"
}

// SetTOTPSecret 启用 / 更换后台账号的 TOTP（§11.6）。secret 为空表示停用。
func (r *Repository) SetTOTPSecret(ctx context.Context, adminID int64, secret string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE admin_users SET totp_secret = NULLIF($2, ''), updated_at = now()
		WHERE id = $1`, adminID, secret)
	return err
}

// TOTPSecret 返回当前生效的密钥（可为空）。
func (r *Repository) TOTPSecret(ctx context.Context, adminID int64) (string, error) {
	var secret string
	err := r.db.QueryRow(ctx, `SELECT COALESCE(totp_secret, '') FROM admin_users WHERE id = $1`,
		adminID).Scan(&secret)
	return secret, err
}

// ── 系统设置（§7.6：热门榜权重不在代码里写死）─────────────────────

// SystemSetting 是 system_settings 的键值视图；值由调用方按用途解析。
type SystemSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HotWeights 返回热门榜聚合权重；缺行时回落默认值（与迁移种子一致）。
func (r *Repository) HotWeights(ctx context.Context) (play, like float64, err error) {
	play, like = 1, 3
	rows, err := r.db.Query(ctx, `
		SELECT key, value FROM system_settings WHERE key IN ('hot.play_weight', 'hot.like_weight')`)
	if err != nil {
		return play, like, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return play, like, err
		}
		parsed, parseErr := strconv.ParseFloat(value, 64)
		if parseErr != nil || parsed < 0 || parsed > 1000 {
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

// SetHotWeights 幂等 upsert 两个权重键；更新人与时间供审计。
func (r *Repository) SetHotWeights(ctx context.Context, adminID int64, play, like float64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO system_settings (key, value, updated_by, updated_at)
		VALUES
		  ('hot.play_weight', $1, $3, now()),
		  ('hot.like_weight', $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET
		  value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		strconv.FormatFloat(play, 'f', -1, 64),
		strconv.FormatFloat(like, 'f', -1, 64),
		adminID)
	return err
}

type ReportSubmission struct {
	CaseID      int64 `json:"case_id"`
	ReportID    int64 `json:"report_id"`
	ReportCount int   `json:"report_count"`
	Merged      bool  `json:"merged"`
}

// SubmitReport merges reports on the same object within 24 hours into one case.
func (r *Repository) SubmitReport(ctx context.Context, reporterID int64,
	objectType string, objectID int64, reason, detail string) (ReportSubmission, error) {
	validReasons := map[string]bool{"vulgar": true, "copyright": true,
		"harassment": true, "fraud": true, "other": true,
		"dead_video": true}
	if !validReasons[reason] {
		return ReportSubmission{}, fmt.Errorf("%w: 举报理由无效", ErrInvalidInput)
	}
	if objectID <= 0 {
		return ReportSubmission{}, fmt.Errorf("%w: 举报对象无效", ErrInvalidInput)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return ReportSubmission{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var caseID int64
	var count int
	merged := false
	err = tx.QueryRow(ctx, `
		SELECT id, report_count FROM report_cases
		WHERE object_type = $1 AND object_id = $2 AND status = 'pending'
		  AND opened_at > now() - interval '24 hours'
		ORDER BY opened_at DESC LIMIT 1 FOR UPDATE`, objectType, objectID).Scan(&caseID, &count)
	if err == nil {
		merged = true
		count++
		if _, err := tx.Exec(ctx, `UPDATE report_cases SET report_count = $2 WHERE id = $1`,
			caseID, count); err != nil {
			return ReportSubmission{}, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ReportSubmission{}, err
	} else {
		count = 1
		if err := tx.QueryRow(ctx, `
			INSERT INTO report_cases (object_type, object_id, report_count)
			VALUES ($1, $2, 1) RETURNING id`, objectType, objectID).Scan(&caseID); err != nil {
			return ReportSubmission{}, err
		}
	}
	var reportID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO reports (reporter_id, object_type, object_id, reason, detail, status, case_id)
		VALUES ($1, $2, $3, $4, $5, 'pending', $6) RETURNING id`,
		reporterID, objectType, objectID, reason, detail, caseID).Scan(&reportID); err != nil {
		return ReportSubmission{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReportSubmission{}, err
	}
	return ReportSubmission{CaseID: caseID, ReportID: reportID,
		ReportCount: count, Merged: merged}, nil
}
