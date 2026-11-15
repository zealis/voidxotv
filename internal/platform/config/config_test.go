package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("YJ_ENV", "")
	t.Setenv("YJ_DATABASE_URL", "postgres://yejie:secret@127.0.0.1:5432/yejie?sslmode=disable")
	t.Setenv("YJ_REDIS_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "dev" || cfg.RedisAddr != "127.0.0.1:6379" {
		t.Fatalf("unexpected defaults: env=%q redis=%q", cfg.Env, cfg.RedisAddr)
	}
	// 默认不信任任何代理头：只读 TCP 对端，避免 X-Forwarded-For 被伪造。
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("trusted proxies should default to empty, got %v", cfg.TrustedProxies)
	}
}

func TestTrustedProxiesFromEnv(t *testing.T) {
	t.Setenv("YJ_ENV", "")
	t.Setenv("YJ_DATABASE_URL", "postgres://yejie:secret@127.0.0.1:5432/yejie?sslmode=disable")
	t.Setenv("YJ_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.5")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %v, want 2 entries", cfg.TrustedProxies)
	}
	if cfg.TrustedProxies[0].String() != "10.0.0.0/8" ||
		cfg.TrustedProxies[1].String() != "192.168.1.5/32" {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxies)
	}
}

func TestProductionRequiresSecureCookie(t *testing.T) {
	t.Setenv("YJ_ENV", "production")
	t.Setenv("YJ_DATABASE_URL", "postgres://yejie:secret@127.0.0.1:5432/yejie")
	t.Setenv("YJ_REDIS_ADDR", "127.0.0.1:6379")
	t.Setenv("YJ_COOKIE_SECURE", "false")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("expected cookie validation error, got %v", err)
	}
}

func TestSessionTTLs(t *testing.T) {
	t.Setenv("YJ_DATABASE_URL", "postgres://yejie:secret@127.0.0.1:5432/yejie")
	t.Setenv("YJ_REDIS_ADDR", "127.0.0.1:6379")
	t.Setenv("YJ_SESSION_IDLE_TTL", "200h")
	t.Setenv("YJ_SESSION_ABSOLUTE_TTL", "168h")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "idle TTL") {
		t.Fatalf("expected TTL validation error, got %v", err)
	}
}

func TestInvalidOrigin(t *testing.T) {
	t.Setenv("YJ_DATABASE_URL", "postgres://yejie:secret@127.0.0.1:5432/yejie")
	t.Setenv("YJ_REDIS_ADDR", "127.0.0.1:6379")
	t.Setenv("YJ_ALLOWED_ORIGINS", "not-a-url")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "allowed origin") {
		t.Fatalf("expected origin validation error, got %v", err)
	}
}
