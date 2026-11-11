package notifications

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 通知类别（§9.1）：互动 / 关注 / 系统。
// 库里的 kind 保留细分值（moderation、report、account、appeal…），
// 只有这里的分类决定设置页的开关是否放行。
const (
	KindInteraction = "interaction"
	KindFollow      = "follow"
	KindSystem      = "system"
)

// Queryer 是 *pgxpool.Pool 与 pgx.Tx 的公共子集。
// 处置类通知必须和业务写入在同一事务里，才能保证「处置成功 ⇔ 通知存在」。
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Message struct {
	UserID int64
	// Kind 是入库的细分类型（moderation / appeal / report / account / interaction），
	// 通知中心按它显示标签。
	Kind string
	// ActorID 是触发通知的用户（点赞者 / 评论者 / 关注者），
	// 通知列表按它批量补全头像；0 表示系统通知这类没有来源用户的消息。
	ActorID int64
	// Category 决定偏好开关；留空时按 Kind 推导。
	// 审核/申诉/举报等细分类型统一归入 KindSystem，才能在设置页用「系统通知」统一控制。
	Category string
	Title    string
	Body     string
	// DedupeKey 非空时，同一用户同一键在 DedupeWindow 内只写一条。
	// 用于「反复取消点赞再点赞」这类高频动作，避免把作者的通知列表刷满。
	DedupeKey    string
	DedupeWindow time.Duration
}

// cst 固定东八区：免打扰按用户本地时间判断，不依赖服务器时区或 tzdata。
var cst = time.FixedZone("CST", 8*60*60)

type prefs struct {
	interaction bool
	follow      bool
	system      bool
	quietStart  int
	quietEnd    int
}

func (p prefs) allows(kind string) bool {
	switch category(kind) {
	case KindInteraction:
		return p.interaction
	case KindFollow:
		return p.follow
	default:
		return p.system
	}
}

// inQuiet 判断 now 是否落在免打扰区间。start == end 表示关闭。
// 跨零点（22:00–08:00）时区间回绕，必须按「或」判断。
func (p prefs) inQuiet(now time.Time) bool {
	if p.quietStart == p.quietEnd {
		return false
	}
	minute := now.Hour()*60 + now.Minute()
	if p.quietStart < p.quietEnd {
		return minute >= p.quietStart && minute < p.quietEnd
	}
	return minute >= p.quietStart || minute < p.quietEnd
}

func category(kind string) string {
	switch kind {
	case KindInteraction:
		return KindInteraction
	case KindFollow:
		return KindFollow
	default:
		return KindSystem
	}
}

func (m Message) category() string {
	if m.Category != "" {
		return m.Category
	}
	return category(m.Kind)
}

// Dispatch 应用用户的通知偏好后写入站内通知，返回是否真的写入。
//   - 类别开关关闭 → 不写；
//   - 免打扰时段内只压制互动 / 关注，系统通知（审核、申诉、举报、账号）始终送达（§9.4）；
//   - 去重窗口内重复动作 → 不写。
func Dispatch(ctx context.Context, q Queryer, msg Message) (bool, error) {
	return DispatchAt(ctx, q, msg, time.Now().In(cst))
}

// DispatchAt 供测试注入固定时间，避免依赖真实时钟。
func DispatchAt(ctx context.Context, q Queryer, msg Message, now time.Time) (bool, error) {
	if msg.UserID <= 0 || msg.Title == "" {
		return false, nil
	}
	value, err := loadPrefs(ctx, q, msg.UserID)
	if err != nil {
		return false, err
	}
	if !value.allows(msg.category()) {
		return false, nil
	}
	if msg.category() != KindSystem && value.inQuiet(now) {
		return false, nil
	}
	if msg.DedupeKey != "" && msg.DedupeWindow > 0 {
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM user_notifications
			  WHERE user_id = $1 AND dedupe_key = $2 AND created_at > $3
			)`, msg.UserID, msg.DedupeKey, now.Add(-msg.DedupeWindow)).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO user_notifications (user_id, kind, category, actor_id, title, body, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		msg.UserID, msg.Kind, msg.category(), nullableID(msg.ActorID),
		msg.Title, msg.Body, msg.DedupeKey); err != nil {
		return false, err
	}
	return true, nil
}

// nullableID 把「无来源用户」的 0 映射成 SQL NULL。
func nullableID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func loadPrefs(ctx context.Context, q Queryer, userID int64) (prefs, error) {
	value := prefs{interaction: true, follow: true, system: true}
	err := q.QueryRow(ctx, `
		SELECT notify_interaction, notify_follow, notify_system, quiet_start, quiet_end
		FROM user_settings WHERE user_id = $1`, userID).Scan(
		&value.interaction, &value.follow, &value.system,
		&value.quietStart, &value.quietEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return prefs{interaction: true, follow: true, system: true}, nil
	}
	if err != nil {
		return value, err
	}
	return value, nil
}
