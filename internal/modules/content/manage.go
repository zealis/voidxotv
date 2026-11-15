package content

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/spam"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 作品管理（§11.1 创作者中心 · 作品管理）：改可见性 / 标题 / 简介，软删除。
// 已发布内容不适用 UpdateDraft（那只允许草稿），因此单独开一条管理路径。

type ManageInput struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

func (s *Service) Manage(ctx context.Context, contentID, authorID int64, input ManageInput) (Content, error) {
	if input.Title != nil && utf8.RuneCountInString(*input.Title) > maxTitleRunes {
		return Content{}, errors.Join(ErrInvalidInput, ErrTitleTooLong)
	}
	if input.Description != nil && utf8.RuneCountInString(*input.Description) > maxDescriptionRunes {
		return Content{}, errors.Join(ErrInvalidInput, ErrBodyTooLong)
	}
	if input.Visibility != nil && !validVisibility(*input.Visibility) {
		return Content{}, ErrVisibility
	}
	if input.Title == nil && input.Description == nil && input.Visibility == nil {
		return Content{}, ErrInvalidInput
	}
	return s.repo.Manage(ctx, contentID, authorID, input)
}

func (s *Service) Delete(ctx context.Context, contentID, authorID int64) error {
	return s.repo.SoftDelete(ctx, contentID, authorID)
}

func (r *Repository) Manage(ctx context.Context, contentID, authorID int64, input ManageInput) (Content, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Content{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	content, err := r.scan(tx.QueryRow(ctx, `
		UPDATE contents SET
		  title = COALESCE($3::text, title),
		  description = COALESCE($4::text, description),
		  visibility = COALESCE($5::text, visibility),
		  updated_at = now()
		WHERE id = $1 AND author_id = $2 AND publish_state <> 'deleted'
		RETURNING `+contentColumns,
		contentID, authorID, input.Title, input.Description, input.Visibility))
	if errors.Is(err, pgx.ErrNoRows) {
		return Content{}, ErrNotFound
	}
	if err != nil {
		return Content{}, fmt.Errorf("content: manage: %w", err)
	}
	// 标题 / 可见性变化会改变落地页与 sitemap，必须原子失效（§10.3）。
	if err := enqueueContentUpdate(ctx, tx, contentID, content.Slug); err != nil {
		return Content{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Content{}, err
	}
	return content, nil
}

// SoftDelete 只标记 publish_state='deleted'：统计、举报、申诉仍保留引用。
func (r *Repository) SoftDelete(ctx context.Context, contentID, authorID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var slug string
	err = tx.QueryRow(ctx, `
		UPDATE contents SET publish_state = 'deleted', updated_at = now()
		WHERE id = $1 AND author_id = $2 AND publish_state <> 'deleted'
		RETURNING slug`, contentID, authorID).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("content: delete: %w", err)
	}
	if err := enqueueContentUpdate(ctx, tx, contentID, slug); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// enqueueContentUpdate 让 Delivery 重新验证该作品页 / 分类页 / sitemap。
// 事件类型沿用 content.updated；消费端只按 payload 的 content_id 解析。
func enqueueContentUpdate(ctx context.Context, tx pgx.Tx, contentID int64, slug string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_key, event_type, payload)
		VALUES ($1, 'content.updated',
		  jsonb_build_object('content_id', $2::bigint, 'slug', $3::text))
		ON CONFLICT (event_key) DO NOTHING`,
		fmt.Sprintf("content.updated:%d:%d", contentID, time.Now().UnixNano()),
		contentID, slug); err != nil {
		return fmt.Errorf("content: enqueue update: %w", err)
	}
	return nil
}

func (h *Handler) manage(w http.ResponseWriter, r *http.Request) {
	var input ManageInput
	if err := httpserver.DecodeJSON(r, &input); err != nil {
		h.writeError(w, r, err)
		return
	}
	fields := make([]spam.Field, 0, 2)
	if input.Title != nil {
		fields = append(fields, spam.Field{Label: "标题", Value: *input.Title})
	}
	if input.Description != nil {
		fields = append(fields, spam.Field{Label: "作品简介", Value: *input.Description})
	}
	if spam.Guard(w, r, fields...) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	content, err := h.svc.Manage(r.Context(), id, principal.UserID, input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, content)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	// 删除作品属于敏感操作：要求重新验证过密码（5 分钟内有效）。
	if !h.auth.RequireStepUp(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	if err := h.svc.Delete(r.Context(), id, principal.UserID); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ── 草稿箱批量删除（§10 / §11.1 列表型页面的批量操作）──────────────

const maxBatchDelete = 50

func (s *Service) DeleteMany(ctx context.Context, ids []int64, authorID int64) ([]int64, []int64, error) {
	return s.repo.SoftDeleteMany(ctx, ids, authorID)
}

// SoftDeleteMany 只删本人且未删除的内容；返回成功删除与不存在/越权的 id，
// 让调用方能看到部分成功而不是整批失败。每条删除都写 content.updated。
func (r *Repository) SoftDeleteMany(ctx context.Context, ids []int64, authorID int64) ([]int64, []int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	type deletedRow struct {
		id   int64
		slug string
	}
	// 先把 RETURNING 读干净再写 outbox：同一个事务连接上不能边读 rows 边执行新语句。
	rows, err := tx.Query(ctx, `
		UPDATE contents SET publish_state = 'deleted', updated_at = now()
		WHERE id = ANY($1) AND author_id = $2 AND publish_state <> 'deleted'
		RETURNING id, slug`, ids, authorID)
	if err != nil {
		return nil, nil, fmt.Errorf("content: batch delete: %w", err)
	}
	var deletedRows []deletedRow
	for rows.Next() {
		var row deletedRow
		if err := rows.Scan(&row.id, &row.slug); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("content: batch delete scan: %w", err)
		}
		deletedRows = append(deletedRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("content: batch delete rows: %w", err)
	}
	rows.Close()

	deleted := make([]int64, 0, len(deletedRows))
	hit := make(map[int64]bool, len(deletedRows))
	for _, row := range deletedRows {
		deleted = append(deleted, row.id)
		hit[row.id] = true
		if err := enqueueContentUpdate(ctx, tx, row.id, row.slug); err != nil {
			return nil, nil, err
		}
	}
	missing := make([]int64, 0)
	for _, id := range ids {
		if !hit[id] {
			missing = append(missing, id)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return deleted, missing, nil
}

type batchDeleteRequest struct {
	IDs []int64 `json:"ids"`
}

func (h *Handler) batchRemove(w http.ResponseWriter, r *http.Request) {
	if !h.auth.RequireStepUp(w, r) {
		return
	}
	var request batchDeleteRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(request.IDs) == 0 || len(request.IDs) > maxBatchDelete {
		h.writeError(w, r, fmt.Errorf("%w：一次可删除 1–%d 条", ErrInvalidInput, maxBatchDelete))
		return
	}
	seen := make(map[int64]bool, len(request.IDs))
	ids := make([]int64, 0, len(request.IDs))
	for _, id := range request.IDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		h.writeError(w, r, ErrInvalidInput)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	deleted, missing, err := h.svc.DeleteMany(r.Context(), ids, principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"deleted": len(deleted), "ids": deleted, "missing": missing,
	})
}
