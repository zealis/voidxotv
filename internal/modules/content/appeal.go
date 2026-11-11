package content

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 申诉相关错误（§9.4 驳回/下架须允许申诉）。
var (
	ErrNotAppealable = errors.New("content is not appealable")
	ErrAppealExists  = errors.New("an open appeal already exists")
	ErrAppealReason  = errors.New("appeal reason must be 5-500 characters")
)

const (
	minAppealRunes = 5
	maxAppealRunes = 500
)

// AppealableStatuses 限定可申诉的审核结论：驳回、下架、限流。
// （「限流」保留落地页但退出推荐，同样属于对作者可见的处置，允许申诉。）
var AppealableStatuses = map[string]bool{
	"rejected":   true,
	"taken_down": true,
	"limited":    true,
}

type Appeal struct {
	ID           int64      `json:"id"`
	ContentID    int64      `json:"content_id"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	DecisionNote string     `json:"decision_note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
}

// CreateAppeal records one appeal per content while a previous one is pending.
// The partial unique index is the source of truth, so concurrent submits can
// never create two open appeals.
func (r *Repository) CreateAppeal(ctx context.Context, contentID, authorID int64,
	decisionID *int64, reason string) (Appeal, error) {

	var appeal Appeal
	err := r.db.QueryRow(ctx, `
		INSERT INTO content_appeals (content_id, author_id, decision_id, reason)
		VALUES ($1, $2, $3, $4)
		RETURNING id, content_id, reason, status, decision_note, created_at, reviewed_at`,
		contentID, authorID, decisionID, reason).Scan(
		&appeal.ID, &appeal.ContentID, &appeal.Reason, &appeal.Status,
		&appeal.DecisionNote, &appeal.CreatedAt, &appeal.ReviewedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Appeal{}, ErrAppealExists
		}
		return Appeal{}, fmt.Errorf("content: create appeal: %w", err)
	}
	return appeal, nil
}

// LatestDecisionID returns the most recent moderation decision for a content.
func (r *Repository) LatestDecisionID(ctx context.Context, contentID int64) (*int64, error) {
	var id int64
	err := r.db.QueryRow(ctx, `
		SELECT id FROM content_review_decisions
		WHERE content_id = $1 ORDER BY id DESC LIMIT 1`, contentID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("content: latest decision: %w", err)
	}
	return &id, nil
}

// Appeals lists the author-visible appeal history of one content.
func (r *Repository) Appeals(ctx context.Context, contentID int64) ([]Appeal, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, content_id, reason, status, decision_note, created_at, reviewed_at
		FROM content_appeals WHERE content_id = $1 ORDER BY id DESC`, contentID)
	if err != nil {
		return nil, fmt.Errorf("content: list appeals: %w", err)
	}
	defer rows.Close()

	result := []Appeal{}
	for rows.Next() {
		var item Appeal
		if err := rows.Scan(&item.ID, &item.ContentID, &item.Reason, &item.Status,
			&item.DecisionNote, &item.CreatedAt, &item.ReviewedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Appeal validates ownership and state, then records the appeal.
func (s *Service) Appeal(ctx context.Context, contentID, authorID int64, reason string) (Appeal, error) {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) < minAppealRunes ||
		utf8.RuneCountInString(reason) > maxAppealRunes {
		return Appeal{}, ErrAppealReason
	}
	item, err := s.repo.GetByID(ctx, contentID)
	if err != nil {
		return Appeal{}, err
	}
	// 非作者一律按不存在处理，不暴露他人内容状态。
	if item.AuthorID != authorID {
		return Appeal{}, ErrNotFound
	}
	if !AppealableStatuses[item.ReviewStatus] {
		return Appeal{}, ErrNotAppealable
	}
	decisionID, err := s.repo.LatestDecisionID(ctx, contentID)
	if err != nil {
		return Appeal{}, err
	}
	appeal, err := s.repo.CreateAppeal(ctx, contentID, authorID, decisionID, reason)
	if errors.Is(err, ErrAppealExists) {
		return Appeal{}, ErrAppealExists
	}
	return appeal, err
}

// ListAppeals returns the appeal history for an author-owned content item.
func (s *Service) ListAppeals(ctx context.Context, contentID, authorID int64) ([]Appeal, error) {
	item, err := s.repo.GetByID(ctx, contentID)
	if err != nil {
		return nil, err
	}
	if item.AuthorID != authorID {
		return nil, ErrNotFound
	}
	return s.repo.Appeals(ctx, contentID)
}

// MyAppeals returns the latest appeal for each of the author's contents, so the
// clients can render "已申诉" without N+1 requests.
func (s *Service) MyAppeals(ctx context.Context, authorID int64) ([]Appeal, error) {
	rows, err := s.repo.db.Query(ctx, `
		SELECT DISTINCT ON (content_id)
		       id, content_id, reason, status, decision_note, created_at, reviewed_at
		FROM content_appeals
		WHERE author_id = $1
		ORDER BY content_id, id DESC`, authorID)
	if err != nil {
		return nil, fmt.Errorf("content: my appeals: %w", err)
	}
	defer rows.Close()

	result := []Appeal{}
	for rows.Next() {
		var item Appeal
		if err := rows.Scan(&item.ID, &item.ContentID, &item.Reason, &item.Status,
			&item.DecisionNote, &item.CreatedAt, &item.ReviewedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
