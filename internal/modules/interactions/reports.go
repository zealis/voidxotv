package interactions

import (
	"net/http"
	"time"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// MyReport 是举报人对自己的举报记录可见的视图（§9.5 处置结果需反馈举报人）。
type MyReport struct {
	ID           int64      `json:"id"`
	ObjectType   string     `json:"object_type"`
	ObjectID     int64      `json:"object_id"`
	ObjectTitle  string     `json:"object_title,omitempty"`
	Reason       string     `json:"reason"`
	Detail       string     `json:"detail"`
	Status       string     `json:"status"`
	CaseStatus   string     `json:"case_status,omitempty"`
	Resolution   string     `json:"resolution,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	CaseResolved *time.Time `json:"case_resolved_at,omitempty"`
}

// myReports lets reporters follow the outcome of what they reported, instead of
// relying on a single one-off notification.
func (h *Handler) myReports(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	rows, err := h.db.Query(r.Context(), `
		SELECT r.id, r.object_type, r.object_id, r.reason, r.detail, r.status,
		       r.created_at, r.resolved_at,
		       COALESCE(CASE
		         WHEN r.object_type = 'content' THEN
		           (SELECT title FROM contents WHERE id = r.object_id)
		         WHEN r.object_type = 'comment' THEN
		           (SELECT left(regexp_replace(btrim(cm.body),
		             '\[\[gif:[^\]]*\]\]', '[动图]', 'g'), 60)
		            FROM comments cm WHERE cm.id = r.object_id)
		         ELSE NULL END, ''),
		       COALESCE(rc.status, ''), COALESCE(rc.resolution, ''), rc.resolved_at
		FROM reports r
		LEFT JOIN report_cases rc ON rc.id = r.case_id
		WHERE r.reporter_id = $1
		ORDER BY r.id DESC LIMIT 50`, principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer rows.Close()

	items := []MyReport{}
	for rows.Next() {
		var item MyReport
		if err := rows.Scan(&item.ID, &item.ObjectType, &item.ObjectID, &item.Reason,
			&item.Detail, &item.Status, &item.CreatedAt, &item.ResolvedAt,
			&item.ObjectTitle, &item.CaseStatus, &item.Resolution,
			&item.CaseResolved); err != nil {
			h.fail(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}
