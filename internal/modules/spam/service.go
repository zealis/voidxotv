package spam

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"github.com/yejie/voidxotv/internal/platform/textfilter"
)

// 配置读缓存的刷新间隔。后台保存 / 下载会立即 Reload，这里的轮询只是让
// 同一份配置在多个 API 实例之间（或外部改库后）最终一致。
const pollInterval = 30 * time.Second

// Field 是一个待审核的自由文本字段。Label 用中文，命中时用来告诉用户该改哪里。
type Field struct {
	Label string
	Value string
}

// BlockedError 是一次明确的内容拒绝，不是系统故障。
type BlockedError struct {
	Label string
	Hits  []string
}

func (e *BlockedError) Error() string {
	label := e.Label
	if label == "" {
		label = "内容"
	}
	return label + "包含不适当用语，请修改后重试"
}

// WriteBlocked 把拒绝理由写成前端能直接展示的中文 message。
// 错误码单独给 content_blocked，前端可据此做更细的交互（不改文案也够用）。
func WriteBlocked(w http.ResponseWriter, r *http.Request, err error) {
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		httpserver.Error(w, http.StatusInternalServerError, "internal_error", "操作失败",
			httpserver.RequestIDFromContext(r.Context()))
		return
	}
	httpserver.Error(w, http.StatusUnprocessableEntity, "content_blocked", blocked.Error(),
		httpserver.RequestIDFromContext(r.Context()))
}

// Service 持有编译后的只读词表；请求路径只做 RLock，不碰数据库（除轮询）。
type Service struct {
	db *pgxpool.Pool

	mu       sync.RWMutex
	enabled  bool
	matcher  *textfilter.Matcher
	revision string
	lastPoll time.Time
}

var (
	globalMu sync.RWMutex
	global   *Service
)

// Init 在 API 启动时装配；读取配置失败不阻断启动，按「未启用」放行。
func Init(ctx context.Context, db *pgxpool.Pool) {
	service := &Service{db: db}
	_ = service.reload(ctx)
	globalMu.Lock()
	global = service
	globalMu.Unlock()
}

func Global() *Service {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// Reload 供后台保存配置 / 下载词库后立刻生效。
func Reload(ctx context.Context) {
	if service := Global(); service != nil {
		_ = service.reload(ctx)
	}
}

func (s *Service) reload(ctx context.Context) error {
	cfg, err := LoadConfig(ctx, s.db)
	if err != nil {
		return err
	}
	lines := append(splitLines(cfg.Words), splitLines(cfg.LexiconText)...)
	compiled := textfilter.Compile(lines)
	s.mu.Lock()
	s.enabled = cfg.Enabled
	s.matcher = compiled
	s.revision = cfg.Revision
	s.lastPoll = time.Now()
	s.mu.Unlock()
	return nil
}

// ensureFresh 只查一个 revision 键；查询失败一律保持旧状态，绝不因配置服务
// 抖动把用户输入全拦下来。
func (s *Service) ensureFresh(ctx context.Context) {
	s.mu.RLock()
	last, current := s.lastPoll, s.revision
	s.mu.RUnlock()
	if time.Since(last) < pollInterval {
		return
	}
	var revision string
	err := s.db.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = 'spam.revision'`).Scan(&revision)
	if err != nil {
		s.mu.Lock()
		s.lastPoll = time.Now()
		s.mu.Unlock()
		return
	}
	if revision != current {
		_ = s.reload(ctx)
		return
	}
	s.mu.Lock()
	s.lastPoll = time.Now()
	s.mu.Unlock()
}

// Guard 是所有写接口共用的入口：命中就直接写好拒绝响应并返回 true，
// 调用方 `if spam.Guard(w, r, ...) { return }` 即可，不必各自重复错误处理。
func Guard(w http.ResponseWriter, r *http.Request, fields ...Field) bool {
	if err := Check(r.Context(), fields...); err != nil {
		WriteBlocked(w, r, err)
		return true
	}
	return false
}

// Check 审核一组字段；未初始化、开关关闭或词表为空时一律放行。
func Check(ctx context.Context, fields ...Field) error {
	service := Global()
	if service == nil {
		return nil
	}
	service.ensureFresh(ctx)
	service.mu.RLock()
	enabled, matcher := service.enabled, service.matcher
	service.mu.RUnlock()
	if !enabled || matcher.Empty() {
		return nil
	}
	for _, field := range fields {
		if field.Value == "" {
			continue
		}
		if hits := matcher.Hits(field.Value); len(hits) > 0 {
			return &BlockedError{Label: field.Label, Hits: hits}
		}
	}
	return nil
}

// Inspect 给后台「测试」用：返回命中了哪些词，不写入任何业务数据。
func Inspect(ctx context.Context, text string) (hits []string, enabled bool) {
	service := Global()
	if service == nil {
		return nil, false
	}
	service.ensureFresh(ctx)
	service.mu.RLock()
	enabled, matcher := service.enabled, service.matcher
	service.mu.RUnlock()
	if !enabled || matcher.Empty() {
		return nil, enabled
	}
	return matcher.Hits(text), true
}
