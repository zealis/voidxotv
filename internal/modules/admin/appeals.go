package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/notifications"
)

// AppealItem is one row of the moderation appeal queue.
type AppealItem struct {
	ID             int64     `json:"id"`
	ContentID      int64     `json:"content_id"`
	Slug           string    `json:"slug"`
	Title          string    `json:"title"`
	Handle         string    `json:"handle"`
	ReviewStatus   string    `json:"review_status"`
	DecisionAction string    `json:"decision_action"`
	DecisionReason string    `json:"decision_reason"`
	Reason         string    `json:"reason"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type AppealDecision struct {
	AppealID    int64  `json:"appeal_id"`
	ContentID   int64  `json:"content_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status"`
	ReviewState string `json:"review_status"`
	Note        string `json:"decision_note,omitempty"`
}

// AppealQueue lists appeals oldest-first (等待时长) for the given status.
func (r *Repository) AppealQueue(ctx context.Context, status string, limit int) ([]AppealItem, error) {
	if status != "pending" && status != "accepted" && status != "rejected" {
		status = "pending"
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT a.id, a.content_id, c.slug, c.title, u.handle, c.review_status,
		       COALESCE(d.action, ''), COALESCE(d.reason, ''), a.reason, a.status, a.created_at
		FROM content_appeals a
		JOIN contents c ON c.id = a.content_id
		JOIN users u ON u.id = a.author_id
		LEFT JOIN content_review_decisions d ON d.id = a.decision_id
		WHERE a.status = $1
		ORDER BY a.created_at ASC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: appeal queue: %w", err)
	}
	defer rows.Close()

	result := []AppealItem{}
	for rows.Next() {
		var item AppealItem
		if err := rows.Scan(&item.ID, &item.ContentID, &item.Slug, &item.Title, &item.Handle,
			&item.ReviewStatus, &item.DecisionAction, &item.DecisionReason,
			&item.Reason, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// DecideAppeal resolves one appeal. Accepting restores the content to approved
// and re-publishes it through the same Outbox path as a manual restore, so
// Delivery/caches behave identically (§9.4 / §10.3).
func (r *Repository) DecideAppeal(ctx context.Context, appealID, adminID int64,
	approve bool, note string) (AppealDecision, error) {

	note = strings.TrimSpace(note)
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return AppealDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var decision AppealDecision
	var authorID int64
	var visibility, reviewStatus string
	err = tx.QueryRow(ctx, `
		SELECT a.content_id, a.author_id, a.status, c.slug, c.title, c.visibility, c.review_status
		FROM content_appeals a
		JOIN contents c ON c.id = a.content_id
		WHERE a.id = $1 FOR UPDATE`, appealID).Scan(
		&decision.ContentID, &authorID, &decision.Status, &decision.Slug,
		&decision.Title, &visibility, &reviewStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppealDecision{}, ErrNotFound
	}
	if err != nil {
		return AppealDecision{}, err
	}
	if decision.Status != "pending" {
		return AppealDecision{}, fmt.Errorf("%w: 该申诉已处理", ErrConflict)
	}

	if approve {
		feedEligible := visibility == "public"
		if _, err := tx.Exec(ctx, `
			UPDATE contents SET review_status = 'approved', feed_eligible = $2,
			  recommend_eligible = true, updated_at = now()
			WHERE id = $1`, decision.ContentID, feedEligible); err != nil {
			return AppealDecision{}, err
		}
		var restoreDecisionID int64
		reason := "申诉通过"
		if note != "" {
			reason += "：" + note
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO content_review_decisions (content_id, admin_id, action, reason, risk_score)
			VALUES ($1, $2, 'restore', $3, 0) RETURNING id`,
			decision.ContentID, adminID, reason).Scan(&restoreDecisionID); err != nil {
			return AppealDecision{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (event_key, event_type, payload)
			VALUES ($1, 'content.review_changed',
			  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text, 'action', 'restore'))
			ON CONFLICT (event_key) DO NOTHING`,
			fmt.Sprintf("content.review_changed:%d:%d", decision.ContentID, restoreDecisionID),
			decision.ContentID, decision.Slug); err != nil {
			return AppealDecision{}, err
		}
		decision.ReviewState = "approved"
		if err := notifyTx(ctx, tx, authorID, "申诉已通过",
			fmt.Sprintf("《%s》的申诉已通过，作品已恢复公开。", decision.Slug)); err != nil {
			return AppealDecision{}, err
		}
	} else {
		decision.ReviewState = reviewStatus
		body := fmt.Sprintf("《%s》的申诉未通过。", decision.Slug)
		if note != "" {
			body += "说明：" + note
		}
		if err := notifyTx(ctx, tx, authorID, "申诉未通过", body); err != nil {
			return AppealDecision{}, err
		}
	}

	status := "rejected"
	if approve {
		status = "accepted"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE content_appeals SET status = $2, reviewer_id = $3, decision_note = $4,
		  reviewed_at = now(), updated_at = now()
		WHERE id = $1`, appealID, status, adminID, note); err != nil {
		return AppealDecision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppealDecision{}, err
	}
	decision.AppealID, decision.Status, decision.Note = appealID, status, note
	return decision, nil
}

// notifyTx writes an in-app notification inside the caller's transaction.
// 申诉结果属于「系统通知」，走统一的偏好判定（§9.1 / §9.4）。
func notifyTx(ctx context.Context, tx pgx.Tx, userID int64, title, body string) error {
	_, err := notifications.Dispatch(ctx, tx, notifications.Message{
		UserID:   userID,
		Kind:     "appeal",
		Category: notifications.KindSystem,
		Title:    title,
		Body:     body,
	})
	return err
}
