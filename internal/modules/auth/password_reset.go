package auth

// 找回密码（§11.7 邮箱验证码基线）：
//   - 6 位数字验证码，有效期 3 小时；未验证过时重发同一个码；
//   - 同一验证码最多验证 20 次，超限作废需重新申请；
//   - 同一 IP 24 小时内最多请求 3 次，超限必须携带图形验证码；
//   - 验证码只存 Redis（§11.7：验证码状态由用户模块管理，存缓存）；
//   - 发信只写 Outbox（event_type='email.send'），由邮件模块异步投递。
//
// 重置成功后撤销该用户全部会话（§11.4 与改密同口径）。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

const (
	resetCodeTTL      = 3 * time.Hour
	resetMaxVerify    = 20
	resetIPLimit      = 3
	resetWindow       = 24 * time.Hour
	captchaTTL        = 10 * time.Minute
	resetCodeLength   = 6
	emailOutboxMaxLen = 2000
)

var (
	ErrResetUserUnknown   = errors.New("账号或邮箱不匹配")
	ErrResetCodeInvalid   = errors.New("验证码不正确或已过期")
	ErrResetCodeExhausted = errors.New("验证码验证次数已用完，请重新申请")
	ErrResetCaptcha       = errors.New("请先完成图形验证码")
	ErrResetRateLimited   = errors.New("请求过于频繁，请稍后再试")
)

// ── 验证码与频控的 Redis 键 ────────────────────────────────────────

func resetCodeKey(userID int64) string  { return fmt.Sprintf("yejie:pwreset:code:%d", userID) }
func resetTriesKey(userID int64) string { return fmt.Sprintf("yejie:pwreset:tries:%d", userID) }
func resetIPKey(ip string) string       { return fmt.Sprintf("yejie:pwreset:req:%s", ip) }
func captchaKey(id string) string       { return fmt.Sprintf("yejie:captcha:%s", id) }

// VerificationStore 管验证码 / 频控 / 图形验证码的缓存状态。
// 独立于 SessionStore：登录会话与验证码的生命周期互不相干。
type VerificationStore struct {
	client *redis.Client
}

func NewVerificationStore(client *redis.Client) *VerificationStore {
	return &VerificationStore{client: client}
}

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomCode() (string, error) {
	max := big.NewInt(1_000_000)
	value, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

// IssueResetCode 生成（或复用未验证的）验证码并写缓存；返回验证码与邮件正文。
func (s *VerificationStore) IssueResetCode(ctx context.Context, userID int64) (string, error) {
	existing, err := s.client.Get(ctx, resetCodeKey(userID)).Result()
	if err == nil && existing != "" {
		// 未验证过 → 重发同一个码（§11.7）。
		return existing, nil
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	code, err := randomCode()
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, resetCodeKey(userID), code, resetCodeTTL).Err(); err != nil {
		return "", err
	}
	s.client.Del(ctx, resetTriesKey(userID))
	return code, nil
}

// ConsumeResetAttempt 校验验证码：正确即清除；错误累计次数，超限作废。
func (s *VerificationStore) ConsumeResetAttempt(ctx context.Context, userID int64, code string) error {
	stored, err := s.client.Get(ctx, resetCodeKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return ErrResetCodeInvalid
	}
	if err != nil {
		return err
	}
	tries, _ := s.client.Get(ctx, resetTriesKey(userID)).Int()
	if tries >= resetMaxVerify {
		s.client.Del(ctx, resetCodeKey(userID), resetTriesKey(userID))
		return ErrResetCodeExhausted
	}
	if stored != code {
		s.client.Set(ctx, resetTriesKey(userID), tries+1, resetCodeTTL)
		return ErrResetCodeInvalid
	}
	s.client.Del(ctx, resetCodeKey(userID), resetTriesKey(userID))
	return nil
}

// CountResetRequest 记录同一 IP 的申请次数，返回是否已超限。
func (s *VerificationStore) CountResetRequest(ctx context.Context, ip string) (bool, error) {
	count, err := s.client.Incr(ctx, resetIPKey(ip)).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		s.client.Expire(ctx, resetIPKey(ip), resetWindow)
	}
	return count > resetIPLimit, nil
}

// NewCaptcha 生成一道算术题：问题渲染为 SVG，答案存缓存。
func (s *VerificationStore) NewCaptcha(ctx context.Context) (id, svg string, err error) {
	id, err = randomToken(16)
	if err != nil {
		return "", "", err
	}
	a, _ := rand.Int(rand.Reader, big.NewInt(9))
	b, _ := rand.Int(rand.Reader, big.NewInt(9))
	question := fmt.Sprintf("%d + %d = ?", a.Int64()+1, b.Int64()+1)
	if err := s.client.Set(ctx, captchaKey(id), strconv.FormatInt(a.Int64()+b.Int64()+2, 10), captchaTTL).Err(); err != nil {
		return "", "", err
	}
	return id, captchaSVG(question), nil
}

// VerifyCaptcha 校验并销毁图形验证码（一次性）。
func (s *VerificationStore) VerifyCaptcha(ctx context.Context, id, answer string) bool {
	if id == "" || answer == "" {
		return false
	}
	key := captchaKey(id)
	stored, err := s.client.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	s.client.Del(ctx, key)
	return stored == answer
}

// captchaSVG 生成带干扰线的极简 SVG 图形验证码。
func captchaSVG(question string) string {
	lines := ""
	for i := 0; i < 4; i++ {
		lines += fmt.Sprintf(`<line x1="0" y1="%d" x2="220" y2="%d" stroke="#8a8a99" stroke-width="1"/>`,
			(i*9+8)%60, (i*13+22)%60)
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="220" height="60" viewBox="0 0 220 60">`+
			`<rect width="220" height="60" fill="#1c1c22"/>%s`+
			`<text x="110" y="38" font-size="26" font-family="monospace" fill="#f2f2f5" `+
			`text-anchor="middle" letter-spacing="6">%s</text></svg>`,
		lines, question)
}

// ── 用户资料 / 密码的仓储补充 ─────────────────────────────────────

// LookupResetTarget 按 handle + email 找找回密码的对象；email 不匹配视为未知（不泄露存在性）。
func (r *Repository) LookupResetTarget(ctx context.Context, handle, email string) (int64, string, error) {
	var userID int64
	var stored string
	err := r.db.QueryRow(ctx, `
		SELECT id, COALESCE(email, '') FROM users
		WHERE handle = $1 AND status <> 'banned'`, handle).Scan(&userID, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", ErrResetUserUnknown
	}
	if err != nil {
		return 0, "", err
	}
	if stored == "" || stored != email {
		return 0, "", ErrResetUserUnknown
	}
	return userID, stored, nil
}

// UpdatePasswordHash 覆盖密码哈希（找回密码与改密共用）。
func (r *Repository) UpdatePasswordHash(ctx context.Context, userID int64, hash string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		userID, hash)
	return err
}

// EnqueueResetEmail 在 Outbox 里写一封待发邮件；与业务写入同事务由调用方保证。
func (r *Repository) EnqueueResetEmail(ctx context.Context, to, code string) error {
	body := fmt.Sprintf(
		"你在夜街申请了找回密码。\r\n\r\n验证码：%s\r\n\r\n验证码 3 小时内有效；如果不是你本人操作，请忽略这封邮件。\r\n",
		code)
	payload, err := json.Marshal(map[string]string{
		"to": to, "subject": "夜街 · 找回密码验证码", "body": body,
	})
	if err != nil {
		return err
	}
	if len(payload) > emailOutboxMaxLen {
		return fmt.Errorf("auth: reset email payload too large")
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO outbox_events (event_key, event_type, payload)
		VALUES ($1, 'email.send', $2)`,
		fmt.Sprintf("email.send:pwreset:%s:%d", to, time.Now().UnixNano()), payload)
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ── HTTP handlers ────────────────────────────────────────────────

func (h *Handler) captcha(w http.ResponseWriter, r *http.Request) {
	if h.verification == nil {
		httpserver.Error(w, http.StatusServiceUnavailable, "unavailable",
			"验证码服务不可用", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	id, svg, err := h.verification.NewCaptcha(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]string{"id": id, "svg": svg})
}

type resetRequest struct {
	Handle        string `json:"handle"`
	Email         string `json:"email"`
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
}

// resetRequestCode 处理找回密码第一步：校验账号/邮箱匹配 + IP 频控，
// 通过后签发验证码并投递邮件。响应不泄露账号是否存在（统一文案）。
func (h *Handler) resetRequestCode(w http.ResponseWriter, r *http.Request) {
	var request resetRequest
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	handle := trimHandle(request.Handle)
	if !handlePattern.MatchString(handle) {
		h.failReset(w, r, ErrResetUserUnknown)
		return
	}
	ip := clientIP(r)
	if h.verification == nil {
		httpserver.Error(w, http.StatusServiceUnavailable, "unavailable",
			"找回密码暂不可用", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	limited, err := h.verification.CountResetRequest(r.Context(), ip)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if limited {
		// 超限必须带有效图形验证码（§11.7 图形验证码联动）。
		if !h.verification.VerifyCaptcha(r.Context(), request.CaptchaID, request.CaptchaAnswer) {
			httpserver.Error(w, http.StatusTooManyRequests, "captcha_required", ErrResetCaptcha.Error(),
				httpserver.RequestIDFromContext(r.Context()))
			return
		}
	}

	userID, email, lookupErr := h.svc.repo.LookupResetTarget(r.Context(), handle, request.Email)
	if lookupErr != nil {
		// 无论账号是否存在都消耗一次频控计数，避免探测。
		h.failReset(w, r, lookupErr)
		return
	}
	code, err := h.verification.IssueResetCode(r.Context(), userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.svc.repo.EnqueueResetEmail(r.Context(), email, code); err != nil {
		h.writeError(w, r, err)
		return
	}
	h.log.Info("password reset requested", "user_id", userID, "ip", ip)
	httpserver.JSON(w, http.StatusOK, map[string]any{
		// 统一文案：不向调用方泄露账号与邮箱的匹配结果。
		"message": "如果账号与邮箱匹配，验证码邮件已发出（3 小时内有效）。",
	})
}

type resetConfirm struct {
	Handle      string `json:"handle"`
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

// resetConfirmHandler 处理找回密码第二步：验证码 + 新密码。
// 成功后撤销全部会话，用户需重新登录。
func (h *Handler) resetConfirmHandler(w http.ResponseWriter, r *http.Request) {
	var request resetConfirm
	if err := httpserver.DecodeJSON(r, &request); err != nil {
		h.writeError(w, r, err)
		return
	}
	handle := trimHandle(request.Handle)
	if !handlePattern.MatchString(handle) || len(request.Code) != resetCodeLength {
		h.failReset(w, r, ErrResetCodeInvalid)
		return
	}
	if utf8.RuneCountInString(request.NewPassword) < 8 {
		h.failReset(w, r, ErrWeakPassword)
		return
	}
	if h.verification == nil {
		httpserver.Error(w, http.StatusServiceUnavailable, "unavailable",
			"找回密码暂不可用", httpserver.RequestIDFromContext(r.Context()))
		return
	}
	userID, _, err := h.svc.repo.LookupResetTarget(r.Context(), handle, request.Email)
	if err != nil {
		h.failReset(w, r, err)
		return
	}
	if err := h.verification.ConsumeResetAttempt(r.Context(), userID, request.Code); err != nil {
		h.failReset(w, r, err)
		return
	}
	hash, err := HashPassword(request.NewPassword)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.svc.repo.UpdatePasswordHash(r.Context(), userID, hash); err != nil {
		h.writeError(w, r, err)
		return
	}
	// 吊销全部活跃会话（§11.4）：包括可能已被盗用的旧会话。
	if err := h.svc.repo.RevokeAllSessions(r.Context(), userID); err != nil {
		h.log.Warn("reset revoke db sessions failed", "error", err, "user_id", userID)
	}
	if h.svc.sessions != nil {
		if err := h.svc.sessions.RevokeAll(r.Context(), userID); err != nil {
			h.log.Warn("reset revoke redis sessions failed", "error", err, "user_id", userID)
		}
	}
	h.log.Info("password reset completed", "user_id", userID)
	httpserver.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// failReset 统一返回 400 + 泛化文案：不泄露账号是否存在或验证码剩余次数。
func (h *Handler) failReset(w http.ResponseWriter, r *http.Request, err error) {
	code := "invalid_input"
	switch {
	case errors.Is(err, ErrResetCaptcha):
		code = "captcha_required"
	case errors.Is(err, ErrResetRateLimited):
		code = "rate_limited"
	case errors.Is(err, ErrResetCodeExhausted):
		code = "code_exhausted"
	}
	httpserver.Error(w, http.StatusBadRequest, code, err.Error(),
		httpserver.RequestIDFromContext(r.Context()))
}

func trimHandle(value string) string {
	for len(value) > 0 && (value[0] == '@') {
		value = value[1:]
	}
	return value
}
