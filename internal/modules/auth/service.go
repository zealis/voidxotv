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
	ErrInvalidHandle      = errors.New("invalid handle")
	ErrWeakPassword       = errors.New("password too short")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrRateLimited        = errors.New("too many login attempts")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrAccountDisabled    = errors.New("account disabled")
)

var handlePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

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

type RegisterInput struct {
	Handle   string
	Email    string
	Password string
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, error) {
	input.Handle = strings.TrimSpace(input.Handle)
	input.Email = strings.TrimSpace(input.Email)
	if !handlePattern.MatchString(input.Handle) {
		return User{}, ErrInvalidHandle
	}
	if utf8.RuneCountInString(input.Password) < 8 {
		return User{}, ErrWeakPassword
	}
	if input.Email != "" && (!strings.Contains(input.Email, "@") || len(input.Email) > 254) {
		return User{}, ErrInvalidEmail
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	return s.repo.CreateUser(ctx, input.Handle, input.Email, hash)
}

type LoginInput struct {
	Handle    string
	Password  string
	DeviceID  string
	IPAddress string
	UserAgent string
}

func (s *Service) Login(ctx context.Context, input LoginInput) (string, User, error) {
	input.Handle = strings.TrimSpace(input.Handle)
	allowed, err := s.limiter.Allow(ctx, input.Handle+"|"+input.IPAddress)
	if err != nil {
		return "", User{}, ErrUnauthorized // Redis failure is fail-closed
	}
	if !allowed {
		return "", User{}, ErrRateLimited
	}
	user, err := s.repo.FindByHandle(ctx, input.Handle)
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
