package notifications

// 数据库集成测试：CI 与本地开发库都提供 PostgreSQL（YJ_DATABASE_URL），
// 缺省时整文件跳过。CI 的 go test 先于迁移步骤执行，所以这里在连接后按需补跑迁移
// （咨询锁串行化，避免并行测试二进制同时迁移）。
// 覆盖：Dispatch 落库（category 推导 / actor_id / 去重 / 偏好压制）、
// 列表分组过滤与未读分桶、单条软删的 404 边界、分组已读。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/migrations"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("YJ_DATABASE_URL")
	if dsn == "" {
		t.Skip("YJ_DATABASE_URL 未设置，跳过数据库集成测试")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	migrateOnce(t, dsn)
	return pool
}

var migrateOnceSync sync.Once

// migrateOnce 在第一次用到数据库时把迁移推到最新；已最新时是 no-op。
func migrateOnce(t *testing.T, dsn string) {
	t.Helper()
	migrateOnceSync.Do(func() {
		if err := applyMigrations(dsn); err != nil {
			t.Fatalf("migrate up: %v", err)
		}
	})
}

func applyMigrations(dsn string) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()
	// 同一个库可能同时被多个包的集成测试连上，咨询锁保证迁移串行。
	if _, err := db.Exec(`SELECT pg_advisory_lock(918273645)`); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer db.Exec(`SELECT pg_advisory_unlock(918273645)`)
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("up: %w", err)
	}
	return nil
}

// fixtureUser 造一个唯一的测试用户；t.Cleanup 里级联清掉它的通知与设置。
func fixtureUser(t *testing.T, db *pgxpool.Pool, tag string) int64 {
	t.Helper()
	handle := fmt.Sprintf("m28t_%s_%d", tag, time.Now().UnixNano())
	var id int64
	if err := db.QueryRow(context.Background(), `
		INSERT INTO users (handle, email, password_hash, display_name)
		VALUES ($1, NULL, 'test-hash', $2) RETURNING id`,
		handle, "M28 测试 "+tag).Scan(&id); err != nil {
		t.Fatalf("fixture user: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func dispatchAt(t *testing.T, db *pgxpool.Pool, msg Message, now time.Time) bool {
	t.Helper()
	written, err := DispatchAt(context.Background(), db, msg, now)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return written
}

func TestDispatchPersistsCategoryAndActor(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	receiver := fixtureUser(t, pool, "recv")
	actor := fixtureUser(t, pool, "actor")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, cst)

	if !dispatchAt(t, pool, Message{
		UserID: receiver, Kind: KindInteraction, ActorID: actor,
		Title: "@actor 赞了你的作品", Body: "《夜》",
	}, now) {
		t.Fatal("interaction notification should be written")
	}
	if !dispatchAt(t, pool, Message{
		UserID: receiver, Kind: KindFollow, ActorID: actor,
		Title: "@actor 关注了你",
	}, now) {
		t.Fatal("follow notification should be written")
	}
	// 系统类（moderation 等）没有来源用户，actor_id 保持 NULL。
	if !dispatchAt(t, pool, Message{
		UserID: receiver, Kind: "moderation", Title: "作品审核通过",
	}, now) {
		t.Fatal("system notification should be written")
	}

	rows, err := pool.Query(ctx, `
		SELECT kind, category, actor_id FROM user_notifications
		WHERE user_id = $1 ORDER BY id`, receiver)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kind, category string
		var actorID *int64
		if err := rows.Scan(&kind, &category, &actorID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch kind {
		case KindInteraction:
			if category != KindInteraction || actorID == nil || *actorID != actor {
				t.Fatalf("interaction row: category=%q actor=%v", category, actorID)
			}
		case KindFollow:
			if category != KindFollow || actorID == nil || *actorID != actor {
				t.Fatalf("follow row: category=%q actor=%v", category, actorID)
			}
		default:
			if category != KindSystem || actorID != nil {
				t.Fatalf("system row: kind=%q category=%q actor=%v", kind, category, actorID)
			}
		}
		got = append(got, kind)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %v", got)
	}
}

func TestDispatchRespectsPreferencesAndQuietHours(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	receiver := fixtureUser(t, pool, "prefs")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, cst)

	// 关闭互动开关：互动被压制，系统通知不受影响（§9.4 处置结论必达）。
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_settings (user_id, notify_interaction, quiet_start, quiet_end)
		VALUES ($1, FALSE, 0, 1439)`, receiver); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if dispatchAt(t, pool, Message{
		UserID: receiver, Kind: KindInteraction, Title: "不该写入",
	}, now) {
		t.Fatal("interaction should be suppressed by preference")
	}
	if !dispatchAt(t, pool, Message{
		UserID: receiver, Kind: "account", Title: "账号状态更新",
	}, now) {
		t.Fatal("system notification must bypass quiet hours")
	}
}

func TestDispatchDedupeWindow(t *testing.T) {
	pool := testPool(t)
	receiver := fixtureUser(t, pool, "dedupe")
	actor := fixtureUser(t, pool, "dedupe_actor")
	// 基准取真实时钟：库里 created_at 用 DB now()，注入时钟只决定去重窗口的
	// 锚点。固定日期会让测试在真实时间越过窗口边界后变成时间炸弹。
	base := time.Now().In(cst)
	msg := Message{
		UserID: receiver, Kind: KindInteraction, ActorID: actor,
		Title: "@actor 赞了你的作品", Body: "《夜》",
		DedupeKey: fmt.Sprintf("like:1:%d", actor), DedupeWindow: 12 * time.Hour,
	}
	if !dispatchAt(t, pool, msg, base) {
		t.Fatal("first dispatch should be written")
	}
	if dispatchAt(t, pool, msg, base.Add(time.Hour)) {
		t.Fatal("duplicate within window should be deduped")
	}
	if !dispatchAt(t, pool, msg, base.Add(13*time.Hour)) {
		t.Fatal("dispatch after window should be written")
	}
}

// newTestHandler 返回带真实池的 handler 与注入 principal 的请求助手。
func newTestHandler(t *testing.T, pool *pgxpool.Pool) (*Handler, func(*testing.T, string, string, *int64) *httptest.ResponseRecorder) {
	t.Helper()
	h := NewHandler(pool, nil, discardLogger())
	request := func(t *testing.T, method, target string, userID *int64) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		if userID != nil {
			req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{UserID: *userID}))
		}
		rec := httptest.NewRecorder()
		// 直接调用方法，绕过 auth 中间件（集成测试只验证模块逻辑本身）。
		switch {
		case req.URL.Path == "/api/v1/notifications":
			h.list(rec, req)
		case req.URL.Path == "/api/v1/notifications/read":
			h.markRead(rec, req)
		default:
			// httptest.NewRequest 不经 ServeMux 路由，PathValue 需手动注入。
			req.SetPathValue("id", strings.TrimPrefix(req.URL.Path, "/api/v1/notifications/"))
			h.remove(rec, req)
		}
		return rec
	}
	return h, request
}

func envelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data
}

func TestListGroupsBucketsAndDelete(t *testing.T) {
	pool := testPool(t)
	receiver := fixtureUser(t, pool, "list")
	actor := fixtureUser(t, pool, "list_actor")
	stranger := fixtureUser(t, pool, "stranger")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, cst)

	dispatchAt(t, pool, Message{UserID: receiver, Kind: KindInteraction, ActorID: actor,
		Title: "互动一"}, now)
	dispatchAt(t, pool, Message{UserID: receiver, Kind: KindInteraction, ActorID: actor,
		Title: "互动二"}, now.Add(time.Second))
	dispatchAt(t, pool, Message{UserID: receiver, Kind: KindFollow, ActorID: actor,
		Title: "关注一"}, now.Add(2*time.Second))
	dispatchAt(t, pool, Message{UserID: receiver, Kind: "report",
		Title: "举报反馈"}, now.Add(3*time.Second))

	_, request := newTestHandler(t, pool)

	// 分组过滤：interaction 只出互动，且按 id 倒序。
	rec := request(t, "GET", "/api/v1/notifications?category=interaction", &receiver)
	data := envelope(t, rec)
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("interaction list should have 2 items, got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["category"] != KindInteraction || first["title"] != "互动二" {
		t.Fatalf("newest interaction first, got %v", first)
	}

	// 未读分桶三键恒齐。
	buckets, _ := data["unread_by_category"].(map[string]any)
	if buckets[KindInteraction].(float64) != 2 ||
		buckets[KindFollow].(float64) != 1 || buckets[KindSystem].(float64) != 1 {
		t.Fatalf("buckets = %v", buckets)
	}
	if data["unread"].(float64) != 4 {
		t.Fatalf("unread total = %v", data["unread"])
	}

	// 非法分组 → 400 invalid_input。
	rec = request(t, "GET", "/api/v1/notifications?category=dm", &receiver)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid category should 400, got %d", rec.Code)
	}

	// 软删：从列表与未读分桶消失。
	targetID := int64(first["id"].(float64))
	rec = request(t, "DELETE", fmt.Sprintf("/api/v1/notifications/%d", targetID), &receiver)
	data = envelope(t, rec)
	if data["deleted"] != true {
		t.Fatalf("delete should succeed, got %v", data)
	}
	buckets, _ = data["unread_by_category"].(map[string]any)
	if buckets[KindInteraction].(float64) != 1 || data["unread"].(float64) != 3 {
		t.Fatalf("after delete: unread=%v buckets=%v", data["unread"], buckets)
	}

	// 重复删除 / 别人的通知 / 不存在 → 404。
	rec = request(t, "DELETE", fmt.Sprintf("/api/v1/notifications/%d", targetID), &receiver)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete should 404, got %d", rec.Code)
	}
	rec = request(t, "DELETE", fmt.Sprintf("/api/v1/notifications/%d", targetID), &stranger)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign delete should 404, got %d", rec.Code)
	}
	rec = request(t, "DELETE", "/api/v1/notifications/999999999", &receiver)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing delete should 404, got %d", rec.Code)
	}
}

func TestMarkReadScopedByCategory(t *testing.T) {
	pool := testPool(t)
	receiver := fixtureUser(t, pool, "read")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, cst)
	dispatchAt(t, pool, Message{UserID: receiver, Kind: KindInteraction, Title: "互动"}, now)
	dispatchAt(t, pool, Message{UserID: receiver, Kind: KindFollow, Title: "关注"}, now)
	dispatchAt(t, pool, Message{UserID: receiver, Kind: "appeal", Title: "申诉"}, now)

	h, _ := newTestHandler(t, pool)

	// POST body 是 JSON：request helper 只支持无 body，这里单独构造。
	markRead := func(t *testing.T, body string, userID int64) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/v1/notifications/read", strings.NewReader(body))
		req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{UserID: userID}))
		rec := httptest.NewRecorder()
		h.markRead(rec, req)
		return rec
	}

	data := envelope(t, markRead(t, `{"category":"follow"}`, receiver))
	buckets := data["unread_by_category"].(map[string]any)
	if buckets[KindFollow].(float64) != 0 || buckets[KindInteraction].(float64) != 1 {
		t.Fatalf("scoped markRead: %v", buckets)
	}
	data = envelope(t, markRead(t, `{}`, receiver))
	if data["unread"].(float64) != 0 {
		t.Fatalf("mark all: %v", data["unread"])
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
