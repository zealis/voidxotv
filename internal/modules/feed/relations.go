package feed

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// RelationItem 是「关注 / 粉丝 / 赞过我」列表里的一张用户卡片（§10 计数区点进来的列表）。
// 不返回邮箱等私密字段；头像沿用作者页的降级策略（无头像时前端按 id 取占位图）。
type RelationItem struct {
	ID            int64  `json:"id"`
	Handle        string `json:"handle"`
	DisplayName   string `json:"display_name"`
	Bio           string `json:"bio"`
	FollowerCount int64  `json:"follower_count"`
	// Following 是「我是否关注 TA」，FollowedBy 是「TA 是否关注我」。
	// 两个都要给：粉丝列表里要显示「回关」按钮，关注列表里要显示互关。
	Following  bool `json:"following"`
	FollowedBy bool `json:"followed_by"`
}

const relationSelect = `
	u.id, u.handle, u.display_name, u.bio,
	(SELECT count(*) FROM follows fc WHERE fc.followee_id = u.id),
	EXISTS (SELECT 1 FROM follows fme WHERE fme.follower_id = $1 AND fme.followee_id = u.id),
	EXISTS (SELECT 1 FROM follows fback WHERE fback.follower_id = u.id AND fback.followee_id = $1)`

// Relations 返回三类关系列表之一，total 是该类关系的全量条数（分页用 offset）。
func (r *Repository) Relations(ctx context.Context, userID int64, kind string, limit, offset int) ([]RelationItem, int64, error) {
	var (
		query string
		count string
	)
	switch kind {
	case "following":
		query = `SELECT ` + relationSelect + `, f.created_at
			FROM follows f JOIN users u ON u.id = f.followee_id
			WHERE f.follower_id = $1 AND u.status = 'active'
			ORDER BY f.created_at DESC, u.id DESC LIMIT $2 OFFSET $3`
		count = `SELECT count(*) FROM follows f JOIN users u ON u.id = f.followee_id
			WHERE f.follower_id = $1 AND u.status = 'active'`
	case "followers":
		query = `SELECT ` + relationSelect + `, f.created_at
			FROM follows f JOIN users u ON u.id = f.follower_id
			WHERE f.followee_id = $1 AND u.status = 'active'
			ORDER BY f.created_at DESC, u.id DESC LIMIT $2 OFFSET $3`
		count = `SELECT count(*) FROM follows f JOIN users u ON u.id = f.follower_id
			WHERE f.followee_id = $1 AND u.status = 'active'`
	case "likes":
		// 赞过我：只统计我自己的、公开且已通过的作品，排除自己给自己的赞。
		query = `SELECT ` + relationSelect + `, max(r.created_at) AS liked_at
			FROM reactions r
			JOIN contents c ON c.id = r.content_id
			JOIN users u ON u.id = r.user_id
			WHERE c.author_id = $1 AND r.kind = 'like' AND r.user_id <> $1
			  AND c.publish_state = 'published' AND c.review_status = 'approved'
			  AND c.visibility = 'public' AND u.status = 'active'
			GROUP BY u.id, u.handle, u.display_name, u.bio
			ORDER BY liked_at DESC, u.id DESC LIMIT $2 OFFSET $3`
		count = `SELECT count(DISTINCT r.user_id)
			FROM reactions r
			JOIN contents c ON c.id = r.content_id
			JOIN users u ON u.id = r.user_id
			WHERE c.author_id = $1 AND r.kind = 'like' AND r.user_id <> $1
			  AND c.publish_state = 'published' AND c.review_status = 'approved'
			  AND c.visibility = 'public' AND u.status = 'active'`
	default:
		return nil, 0, fmt.Errorf("feed: unknown relation kind %q", kind)
	}

	var total int64
	if err := r.db.QueryRow(ctx, count, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("feed: relations count: %w", err)
	}

	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("feed: relations: %w", err)
	}
	defer rows.Close()

	items := []RelationItem{}
	for rows.Next() {
		var item RelationItem
		var actedAt any
		if err := rows.Scan(&item.ID, &item.Handle, &item.DisplayName, &item.Bio,
			&item.FollowerCount, &item.Following, &item.FollowedBy, &actedAt); err != nil {
			return nil, 0, fmt.Errorf("feed: relations scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (h *Handler) relations(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "following"
	}
	if kind != "following" && kind != "followers" && kind != "likes" {
		httpserver.Error(w, http.StatusBadRequest, "invalid_input", "未知的关系类型",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	items, total, err := h.repo.Relations(r.Context(), principal.UserID, kind, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"kind": kind, "items": items, "total": total,
		"has_more": int64(offset+len(items)) < total,
	})
}
