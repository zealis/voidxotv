package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yejie/voidxotv/internal/platform/config"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrWeakPassword       = errors.New("password too short")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrRateLimited        = errors.New("too many login attempts")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrAccountDisabled    = errors.New("account disabled")
)

// 登录只认邮箱，格式校验放在服务层，避免 handler 与 service 两套规则。
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail 统一邮箱形态（去空格、小写）：注册写入与登录查询必须用同一规则，
// 否则 `A@x.com` 注册、`a@x.com` 登录会查不到（唯一索引本身区分大小写）。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidEmail 判断邮箱是否可用于登录标识（长度上限 254 + 基础格式）。
// 前台注册与后台建号共用一套规则，避免两边校验口径不一致。
func ValidEmail(email string) bool {
	return len(email) > 0 && len(email) <= 254 && emailPattern.MatchString(email)
}

// Service implements registration, login, authentication and logout.
type Service struct {
	repo      *Repository
	sessions  *SessionStore
	limiter   *LoginLimiter
	cfg       config.Config
	log       *slog.Logger
	dummyHash string
}

func NewService(repo *Repository, sessions *SessionStore, limiter *LoginLimiter,
	cfg config.Config, log *slog.Logger) *Service {
	// One precomputed hash keeps the missing-user path timing-comparable to a real login.
	dummy, err := HashPassword("yejie-dummy-password")
	if err != nil {
		dummy = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return &Service{repo: repo, sessions: sessions, limiter: limiter, cfg: cfg, log: log, dummyHash: dummy}
}

// RegisterInput 只需要邮箱与密码：夜街号（= 用户 ID）与默认昵称由数据库生成。
type RegisterInput struct {
	Email    string
	Password string
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, error) {
	email := NormalizeEmail(input.Email)
	if !ValidEmail(email) {
		return User{}, ErrInvalidEmail
	}
	if utf8.RuneCountInString(input.Password) < 8 {
		return User{}, ErrWeakPassword
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	return s.repo.CreateUser(ctx, email, hash)
}

type LoginInput struct {
	// Account 是登录标识：前台只提交邮箱；同时接受纯数字夜街号，
	// 让后台脚本与既有调用方（curl、e2e）不必立即跟着改。
	Account   string
	Password  string
	DeviceID  string
	IPAddress string
	UserAgent string
}

func (s *Service) Login(ctx context.Context, input LoginInput) (string, User, error) {
	account := NormalizeEmail(input.Account)
	allowed, err := s.limiter.Allow(ctx, account+"|"+input.IPAddress)
	if err != nil {
		return "", User{}, ErrUnauthorized // Redis failure is fail-closed
	}
	if !allowed {
		return "", User{}, ErrRateLimited
	}
	user, err := s.repo.FindByAccount(ctx, account)
	if errors.Is(err, ErrUserNotFound) {
		_, _ = VerifyPassword(s.dummyHash, input.Password)
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	matched, verifyErr := VerifyPassword(user.PasswordHash, input.Password)
	if verifyErr != nil || !matched {
		return "", User{}, ErrInvalidCredentials
	}
	if user.Status != "active" {
		return "", User{}, ErrAccountDisabled
	}
	if input.DeviceID == "" {
		input.DeviceID = "web"
	}
	token, err := s.sessions.Create(ctx, SessionRecord{
		UserID:   user.ID,
		Handle:   user.Handle,
		Roles:    []string{user.Role},
		DeviceID: input.DeviceID,
	})
	if err != nil {
		return "", User{}, fmt.Errorf("create session: %w", err)
	}
	// Long-term session record is audit-only; a DB hiccup must not break an active session.
	if err := s.repo.RecordSession(ctx, user.ID, TokenHash(token), input.DeviceID,
		input.IPAddress, input.UserAgent, time.Now().UTC().Add(s.cfg.SessionAbsoluteTTL)); err != nil {
		s.log.Warn("long-term session record failed", "error", err, "user_id", user.ID)
	}
	return token, user, nil
}

// Authenticate is fail-closed: any Redis or validation problem becomes unauthorized.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	_, principal, err := s.sessions.Get(ctx, token)
	if err != nil {
		if !errors.Is(err, ErrSessionNotFound) && !errors.Is(err, ErrSessionExpired) {
			s.log.Warn("session lookup failed", "error", err)
		}
		return Principal{}, ErrUnauthorized
	}
	return principal, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	if err := s.sessions.Delete(ctx, principal.SessionToken); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := s.repo.RevokeSession(ctx, TokenHash(principal.SessionToken)); err != nil {
		s.log.Warn("session revoke record failed", "error", err)
	}
	return nil
}
