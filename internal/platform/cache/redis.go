// Package cache owns Redis: sessions, reconstructable caches, rate limits and delivery coordination.
package cache

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/yejie/voidxotv/internal/platform/config"
)

// Store wraps the shared Redis client. Every operation must have an explicit timeout.
type Store struct {
	client *goredis.Client
}

// Open connects and pings Redis; a failed cache must never be treated as an auth bypass.
func Open(ctx context.Context, cfg config.Config) (*Store, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     20,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}
	return &Store{client: client}, nil
}

// Ping is used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("cache: not configured")
	}
	return s.client.Ping(ctx).Err()
}

// Client exposes the shared client to modules; keep every call bounded by a context.
func (s *Store) Client() *goredis.Client {
	if s == nil {
		return nil
	}
	return s.client
}

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
