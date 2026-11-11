package content

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

// 创作者数据看板（§11.1 创作者中心 · 数据看板）。
// 口径：播放来自 content_stats_daily（按天增量），点赞 / 评论按 reactions /
// comments 的时间戳聚合，新增粉丝按 follows.created_at。全部按东八区自然日切分。

var shanghai = time.FixedZone("CST", 8*60*60)

type AnalyticsCounts struct {
	Plays     int64 `json:"plays"`
	Likes     int64 `json:"likes"`
	Comments  int64 `json:"comments"`
	Followers int64 `json:"followers"`
}

type AnalyticsDay struct {
	Day      string `json:"day"`
	Plays    int64  `json:"plays"`
	Likes    int64  `json:"likes"`
	Comments int64  `json:"comments"`
}

type AnalyticsWork struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Plays    int64  `json:"plays"`
	Likes    int64  `json:"likes"`
	Comments int64  `json:"comments"`
}

type Analytics struct {
	Days int `json:"days"`
	// Start / End 是自然日（YYYY-MM-DD），闭区间；前端据此标注横轴。
	Start    string          `json:"start"`
	End      string          `json:"end"`
	Totals   AnalyticsCounts `json:"totals"`
	Previous AnalyticsCounts `json:"previous"`
	Series   []AnalyticsDay  `json:"series"`
	Top      []AnalyticsWork `json:"top"`
	Lifetime AnalyticsCounts `json:"lifetime"`
}

func (s *Service) Analytics(ctx context.Context, userID int64, days int) (Analytics, error) {
	if days != 7 && days != 30 {
		days = 7
	}
	return s.repo.Analytics(ctx, userID, days)
}

func (r *Repository) Analytics(ctx context.Context, userID int64, days int) (Analytics, error) {
	now := time.Now().In(shanghai)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, shanghai)
	start := today.AddDate(0, 0, -(days - 1))
	endExclusive := today.AddDate(0, 0, 1)
	prevStart := start.AddDate(0, 0, -days)
	prevEnd := start

	plays, likes, comments, err := r.dailyCounts(ctx, userID, start, endExclusive)
	if err != nil {
		return Analytics{}, err
	}
	prevPlays, prevLikes, prevComments, err := r.dailyCounts(ctx, userID, prevStart, prevEnd)
	if err != nil {
		return Analytics{}, err
	}

	series := make([]AnalyticsDay, 0, days)
	var totals, previous AnalyticsCounts
	for day := start; day.Before(endExclusive); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		item := AnalyticsDay{
			Day: key, Plays: plays[key], Likes: likes[key], Comments: comments[key],
		}
		totals.Plays += item.Plays
		totals.Likes += item.Likes
		totals.Comments += item.Comments
		series = append(series, item)
	}
	for _, value := range prevPlays {
		previous.Plays += value
	}
	for _, value := range prevLikes {
		previous.Likes += value
	}
	for _, value := range prevComments {
		previous.Comments += value
	}

	if err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM follows
		WHERE followee_id = $1 AND created_at >= $2 AND created_at < $3`,
		userID, start, endExclusive).Scan(&totals.Followers); err != nil {
		return Analytics{}, fmt.Errorf("content: analytics followers: %w", err)
	}
	if err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM follows
		WHERE followee_id = $1 AND created_at >= $2 AND created_at < $3`,
		userID, prevStart, prevEnd).Scan(&previous.Followers); err != nil {
		return Analytics{}, fmt.Errorf("content: analytics prev followers: %w", err)
	}

	lifetime, err := r.lifetimeCounts(ctx, userID)
	if err != nil {
		return Analytics{}, err
	}
	top, err := r.topWorks(ctx, userID, start, endExclusive)
	if err != nil {
		return Analytics{}, err
	}

	return Analytics{
		Days: days, Start: start.Format("2006-01-02"), End: today.Format("2006-01-02"),
		Totals: totals, Previous: previous, Series: series, Top: top, Lifetime: lifetime,
	}, nil
}

// dailyCounts 返回 [from, to) 内每天的三类数据，缺失的日期由调用方补 0。
func (r *Repository) dailyCounts(ctx context.Context, userID int64, from, to time.Time) (map[string]int64, map[string]int64, map[string]int64, error) {
	plays := map[string]int64{}
	likes := map[string]int64{}
	comments := map[string]int64{}

	rows, err := r.db.Query(ctx, `
		SELECT d.day::text, sum(d.plays)::bigint
		FROM content_stats_daily d JOIN contents c ON c.id = d.content_id
		WHERE c.author_id = $1 AND d.day >= $2::date AND d.day < $3::date
		GROUP BY d.day`, userID, from, to)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("content: analytics plays: %w", err)
	}
	for rows.Next() {
		var day string
		var value int64
		if err := rows.Scan(&day, &value); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		plays[day] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	if likes, err = r.dailyByJoin(ctx, `
		SELECT (r.created_at AT TIME ZONE 'Asia/Shanghai')::date::text, count(*)
		FROM reactions r JOIN contents c ON c.id = r.content_id
		WHERE c.author_id = $1 AND r.kind = 'like' AND r.created_at >= $2 AND r.created_at < $3
		GROUP BY 1`, userID, from, to); err != nil {
		return nil, nil, nil, err
	}
	if comments, err = r.dailyByJoin(ctx, `
		SELECT (cm.created_at AT TIME ZONE 'Asia/Shanghai')::date::text, count(*)
		FROM comments cm JOIN contents c ON c.id = cm.content_id
		WHERE c.author_id = $1 AND cm.created_at >= $2 AND cm.created_at < $3
		GROUP BY 1`, userID, from, to); err != nil {
		return nil, nil, nil, err
	}
	return plays, likes, comments, nil
}

func (r *Repository) dailyByJoin(ctx context.Context, query string, userID int64, from, to time.Time) (map[string]int64, error) {
	result := map[string]int64{}
	rows, err := r.db.Query(ctx, query, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("content: analytics daily: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var value int64
		if err := rows.Scan(&day, &value); err != nil {
			return nil, err
		}
		result[day] = value
	}
	return result, rows.Err()
}

func (r *Repository) lifetimeCounts(ctx context.Context, userID int64) (AnalyticsCounts, error) {
	var value AnalyticsCounts
	err := r.db.QueryRow(ctx, `
		SELECT
		  (SELECT COALESCE(sum(cc.plays), 0) FROM content_counters cc
		   JOIN contents c ON c.id = cc.content_id WHERE c.author_id = $1),
		  (SELECT count(*) FROM reactions r JOIN contents c ON c.id = r.content_id
		   WHERE c.author_id = $1 AND r.kind = 'like'),
		  (SELECT count(*) FROM comments cm JOIN contents c ON c.id = cm.content_id
		   WHERE c.author_id = $1),
		  (SELECT count(*) FROM follows WHERE followee_id = $1)`, userID).Scan(
		&value.Plays, &value.Likes, &value.Comments, &value.Followers)
	if err != nil {
		return value, fmt.Errorf("content: analytics lifetime: %w", err)
	}
	return value, nil
}

func (r *Repository) topWorks(ctx context.Context, userID int64, from, to time.Time) ([]AnalyticsWork, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.slug, c.type, c.title,
		  COALESCE((SELECT sum(d.plays) FROM content_stats_daily d
		            WHERE d.content_id = c.id AND d.day >= $2::date AND d.day < $3::date), 0),
		  (SELECT count(*) FROM reactions r
		   WHERE r.content_id = c.id AND r.kind = 'like'
		     AND r.created_at >= $2 AND r.created_at < $3),
		  (SELECT count(*) FROM comments cm
		   WHERE cm.content_id = c.id AND cm.created_at >= $2 AND cm.created_at < $3)
		FROM contents c
		WHERE c.author_id = $1 AND c.publish_state <> 'deleted'
		ORDER BY 5 DESC, 6 DESC, c.id DESC
		LIMIT 5`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("content: analytics top: %w", err)
	}
	defer rows.Close()
	items := []AnalyticsWork{}
	for rows.Next() {
		var item AnalyticsWork
		if err := rows.Scan(&item.ID, &item.Slug, &item.Type, &item.Title,
			&item.Plays, &item.Likes, &item.Comments); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *Handler) myAnalytics(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	days := 7
	if raw := r.URL.Query().Get("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || (value != 7 && value != 30) {
			httpserver.Error(w, http.StatusBadRequest, "invalid_input", "days 只支持 7 或 30",
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
		days = value
	}
	data, err := h.svc.Analytics(r.Context(), principal.UserID, days)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, data)
}
