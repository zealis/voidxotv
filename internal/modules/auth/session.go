package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/yejie/voidxotv/internal/platform/cache"
	"github.com/yejie/voidxotv/internal/platform/config"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
)

const (
	sessionKeyPrefix   = "yejie:session:"
	userSessionsPrefix = "yejie:user-sessions:"
)

// TokenHash stores only a hash; the raw token lives in the cookie alone (开发技术规范 §11.4).
func TokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func sessionKey(token string) string {
	return sessionKeyPrefix + hex.EncodeToString(TokenHash(token))
}

// userSessionsKey 是「用户 → 活跃会话令牌」的集合索引，TTL 取绝对过期上限。
func userSessionsKey(userID int64) string {
	return fmt.Sprintf("%s%d", userSessionsPrefix, userID)
}

// SessionStore owns active sessions in Redis. Redis failure is fail-closed for authentication.
type SessionStore struct {
	client      *goredis.Client
	idleTTL     time.Duration
	absoluteTTL time.Duration
}

func NewSessionStore(store *cache.Store, cfg config.Config) *SessionStore {
	return &SessionStore{
		client:      store.Client(),
		idleTTL:     cfg.SessionIdleTTL,
		absoluteTTL: cfg.SessionAbsoluteTTL,
	}
}

// Create issues a random token and persists the active session with an idle TTL.
func (s *SessionStore) Create(ctx context.Context, record SessionRecord) (string, error) {
	token, err := newToken(32)
	if err != nil {
		return "", err
	}
	csrf, err := newToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	record.CSRF = csrf
	record.CreatedAt = now
	record.AbsoluteExpiresAt = now.Add(s.absoluteTTL)
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, sessionKey(token), payload, s.idleTTL).Err(); err != nil {
		return "", fmt.Errorf("session create: %w", err)
	}
	// 用户级会话索引：改密 / 封禁等需要一次吊销该用户全部会话。
	if err := s.client.SAdd(ctx, userSessionsKey(record.UserID), token, s.absoluteTTL).Err(); err != nil {
		return "", fmt.Errorf("session index: %w", err)
	}
	return token, nil
}

// RevokeAll 删除某用户的全部活跃会话（Redis 是会话有效性的事实来源；
// user_sessions 表的审计记录由调用方按需补写）。
func (s *SessionStore) RevokeAll(ctx context.Context, userID int64) error {
	index := userSessionsKey(userID)
	tokens, err := s.client.SMembers(ctx, index).Result()
	if err != nil {
		return fmt.Errorf("session index read: %w", err)
	}
	for _, token := range tokens {
		if err := s.client.Del(ctx, sessionKey(token)).Err(); err != nil {
			return fmt.Errorf("session revoke: %w", err)
		}
	}
	return s.client.Del(ctx, index).Err()
}

// Get validates both idle (Redis TTL) and absolute expiry, then slides the idle window.
func (s *SessionStore) Get(ctx context.Context, token string) (SessionRecord, Principal, error) {
	var record SessionRecord
	payload, err := s.client.Get(ctx, sessionKey(token)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return record, Principal{}, ErrSessionNotFound
	}
	if err != nil {
		return record, Principal{}, fmt.Errorf("session read: %w", err)
	}
	if err := json.Unmarshal(payload, &record); err != nil {
		return record, Principal{}, fmt.Errorf("session decode: %w", err)
	}
	if now := time.Now().UTC(); !record.AbsoluteExpiresAt.After(now) {
		_ = s.Delete(ctx, token)
		return record, Principal{}, ErrSessionExpired
	}
	_ = s.client.Expire(ctx, sessionKey(token), s.idleTTL).Err()
	return record, Principal{
		UserID:       record.UserID,
		Handle:       record.Handle,
		Roles:        record.Roles,
		DeviceID:     record.DeviceID,
		SessionToken: token,
		CSRF:         record.CSRF,
	}, nil
}

func (s *SessionStore) Delete(ctx context.Context, token string) error {
	return s.client.Del(ctx, sessionKey(token)).Err()
}
