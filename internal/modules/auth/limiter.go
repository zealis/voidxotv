package auth

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// LoginLimiter is a Redis fixed-window counter; failure fails the login (never open).
type LoginLimiter struct {
	client *goredis.Client
	limit  int
	window time.Duration
}

func NewLoginLimiter(client *goredis.Client, limit int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{client: client, limit: limit, window: window}
}

func (l *LoginLimiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := "yejie:ratelimit:login:" + key
	count, err := l.client.Incr(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("login limiter: %w", err)
	}
	if count == 1 {
		if err := l.client.Expire(ctx, redisKey, l.window).Err(); err != nil {
			return false, fmt.Errorf("login limiter expiry: %w", err)
		}
	}
	return count <= int64(l.limit), nil
}
