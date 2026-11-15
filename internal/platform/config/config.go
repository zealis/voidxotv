// Package config loads and validates environment configuration for all 夜街 processes.
// Values come from the process environment only; no .env file is read implicitly.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const envPrefix = "YJ_"

// Config is the validated runtime configuration. Keep secrets out of logs and error pages.
type Config struct {
	Env      string
	HTTPAddr string
	LogLevel string

	DatabaseURL       string
	DBMaxOpenConns    int
	DBMinIdleConns    int
	DBConnMaxLifetime time.Duration

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	WebOrigin      string
	APIOrigin      string
	AllowedOrigins []string
	TrustedProxies []netip.Prefix

	CookieSecure       bool
	SessionIdleTTL     time.Duration
	SessionAbsoluteTTL time.Duration

	S3Endpoint       string
	S3PublicEndpoint string
	S3Bucket         string
	S3Region         string
	S3AccessKey      string
	S3SecretKey      string
	S3PublicRead     bool

	FFmpegPath  string
	FFprobePath string

	WorkerID           string
	WorkerTempDir      string
	WorkerLeaseTTL     time.Duration
	WorkerHeartbeat    time.Duration
	WorkerIdleInterval time.Duration
	TranscodeTimeout   time.Duration
	MaxUploadBytes     int64
	ShortVideoMaxMS    int64
	// M5：HEVC 作为可选流，默认关闭；H.264 始终是强制回退（§5.1）。
	TranscodeHEVC bool

	// M5：推荐排序（§9.3）。rule 为可解释规则排序，newest 为按时间回退。
	FeedRanking       string
	FeedCandidateDays int

	// M3：页面交付与 SEO
	SiteURL              string
	DeliveryToken        string
	DeliveryNuxtOrigin   string
	DeliveryDebounce     time.Duration
	DeliveryHomeDebounce time.Duration
	DeliveryLeaseTTL     time.Duration
	DeliveryIdleInterval time.Duration
	DeliveryMaxAttempts  int
	// OutboxMaxAttempts 是单条 Outbox 事件的尝试上限：超过后进入 failed 终态，
	// 避免内容被物理删除等永久性错误让事件无限重试（§10.4）。
	OutboxMaxAttempts int
	CDNZoneID         string
	CDNAPIToken       string

	// M4：后台、统计聚合与混合来源
	TrustedImportHosts     []string
	StatsInterval          time.Duration
	ImportsInterval        time.Duration
	AdminBootstrapPassword string

	// 邮件通道（§11.7）：自建 SMTP，经 Transactional Outbox 异步投递。
	// Host 为空表示未配置：邮件事件进入死信并记录原因，业务流不阻塞。
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
}

// Load reads the environment and fails fast on invalid values.
func Load() (Config, error) {
	cfg := Config{
		Env:                    env("ENV", "dev"),
		HTTPAddr:               env("HTTP_ADDR", "127.0.0.1:18080"),
		LogLevel:               env("LOG_LEVEL", "info"),
		DatabaseURL:            env("DATABASE_URL", ""),
		DBMaxOpenConns:         intEnv("DB_MAX_OPEN_CONNS", 20),
		DBMinIdleConns:         intEnv("DB_MIN_IDLE_CONNS", 2),
		DBConnMaxLifetime:      durationEnv("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		RedisAddr:              env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:          env("REDIS_PASSWORD", ""),
		RedisDB:                intEnv("REDIS_DB", 0),
		WebOrigin:              env("WEB_ORIGIN", "http://127.0.0.1:3000"),
		APIOrigin:              env("API_ORIGIN", "http://127.0.0.1:18080"),
		AllowedOrigins:         splitCSV(env("ALLOWED_ORIGINS", "http://127.0.0.1:3000")),
		CookieSecure:           boolEnv("COOKIE_SECURE", false),
		SessionIdleTTL:         durationEnv("SESSION_IDLE_TTL", 30*time.Minute),
		SessionAbsoluteTTL:     durationEnv("SESSION_ABSOLUTE_TTL", 168*time.Hour),
		S3Endpoint:             env("S3_ENDPOINT", ""),
		S3PublicEndpoint:       env("S3_PUBLIC_ENDPOINT", ""),
		S3Bucket:               env("S3_BUCKET", ""),
		S3Region:               env("S3_REGION", "us-east-1"),
		S3AccessKey:            env("S3_ACCESS_KEY", ""),
		S3SecretKey:            env("S3_SECRET_KEY", ""),
		S3PublicRead:           boolEnv("S3_PUBLIC_READ", false),
		FFmpegPath:             env("FFMPEG_PATH", "ffmpeg"),
		FFprobePath:            env("FFPROBE_PATH", "ffprobe"),
		WorkerID:               env("WORKER_ID", defaultWorkerID()),
		WorkerTempDir:          env("WORKER_TEMP_DIR", os.TempDir()),
		WorkerLeaseTTL:         durationEnv("WORKER_LEASE_TTL", 60*time.Second),
		WorkerHeartbeat:        durationEnv("WORKER_HEARTBEAT", 20*time.Second),
		WorkerIdleInterval:     durationEnv("WORKER_IDLE_INTERVAL", 5*time.Second),
		TranscodeTimeout:       durationEnv("TRANSCODE_TIMEOUT", 30*time.Minute),
		MaxUploadBytes:         int64(intEnv("MAX_UPLOAD_MB", 2048)) * 1024 * 1024,
		ShortVideoMaxMS:        int64(intEnv("SHORT_VIDEO_MAX_MS", 180000)),
		TranscodeHEVC:          boolEnv("TRANSCODE_HEVC", false),
		FeedRanking:            env("FEED_RANKING", "rule"),
		FeedCandidateDays:      intEnv("FEED_CANDIDATE_DAYS", 30),
		SiteURL:                env("SITE_URL", "http://127.0.0.1:13000"),
		DeliveryToken:          env("DELIVERY_TOKEN", "dev-internal-token"),
		DeliveryNuxtOrigin:     env("DELIVERY_NUXT_ORIGIN", "http://127.0.0.1:13000"),
		DeliveryDebounce:       durationEnv("DELIVERY_DEBOUNCE", 3*time.Second),
		DeliveryHomeDebounce:   durationEnv("DELIVERY_HOME_DEBOUNCE", 5*time.Second),
		DeliveryLeaseTTL:       durationEnv("DELIVERY_LEASE_TTL", 60*time.Second),
		DeliveryIdleInterval:   durationEnv("DELIVERY_IDLE_INTERVAL", 2*time.Second),
		DeliveryMaxAttempts:    intEnv("DELIVERY_MAX_ATTEMPTS", 5),
		OutboxMaxAttempts:      intEnv("DELIVERY_OUTBOX_MAX_ATTEMPTS", 8),
		CDNZoneID:              env("CDN_ZONE_ID", ""),
		CDNAPIToken:            env("CDN_API_TOKEN", ""),
		TrustedImportHosts:     splitCSV(env("TRUSTED_IMPORT_HOSTS", "")),
		StatsInterval:          durationEnv("STATS_INTERVAL", 5*time.Minute),
		ImportsInterval:        durationEnv("IMPORTS_INTERVAL", 5*time.Second),
		AdminBootstrapPassword: env("ADMIN_BOOTSTRAP_PASSWORD", ""),

		SMTPHost:     env("YJ_SMTP_HOST", ""),
		SMTPPort:     intEnv("YJ_SMTP_PORT", 587),
		SMTPUsername: env("YJ_SMTP_USERNAME", ""),
		SMTPPassword: env("YJ_SMTP_PASSWORD", ""),
		SMTPFrom:     env("YJ_SMTP_FROM", ""),
	}
	// 默认空：访客 IP 只读 TCP 对端地址，任何代理头都不采信。
	// 部署在反向代理后面时，把代理网段填进 YJ_TRUSTED_PROXIES（兼容旧名
	// TRUSTED_PROXIES），才会解析 X-Forwarded-For 的最右一跳。
	for _, raw := range splitCSV(env("YJ_TRUSTED_PROXIES", env("TRUSTED_PROXIES", ""))) {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, addrErr := netip.ParseAddr(raw)
			if addrErr != nil {
				return Config{}, fmt.Errorf("config: invalid trusted proxy %q: %w", raw, err)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, prefix)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate enforces the security and consistency invariants declared in 开发技术规范 §2.6/§15.
func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("config: YJ_DATABASE_URL is required")
	}
	if c.RedisAddr == "" {
		return errors.New("config: YJ_REDIS_ADDR is required")
	}
	if c.DBMaxOpenConns < 1 || c.DBMinIdleConns < 0 || c.DBMinIdleConns > c.DBMaxOpenConns {
		return errors.New("config: database pool sizes are invalid")
	}
	if c.SessionIdleTTL <= 0 || c.SessionAbsoluteTTL <= 0 || c.SessionIdleTTL > c.SessionAbsoluteTTL {
		return errors.New("config: session idle TTL must be positive and not exceed absolute TTL")
	}
	if c.IsProduction() && !c.CookieSecure {
		return errors.New("config: YJ_COOKIE_SECURE must be true outside dev/test")
	}
	if c.IsProduction() && (c.DeliveryToken == "" || c.DeliveryToken == "dev-internal-token") {
		return errors.New("config: YJ_DELIVERY_TOKEN must be replaced outside dev/test")
	}
	if c.IsProduction() && c.AdminBootstrapPassword == "" {
		return errors.New("config: YJ_ADMIN_BOOTSTRAP_PASSWORD must be set outside dev/test")
	}
	if c.FeedRanking != "rule" && c.FeedRanking != "newest" {
		return errors.New("config: YJ_FEED_RANKING must be rule or newest")
	}
	if c.FeedCandidateDays < 1 || c.FeedCandidateDays > 365 {
		return errors.New("config: YJ_FEED_CANDIDATE_DAYS must be between 1 and 365")
	}
	for _, origin := range c.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" {
			return fmt.Errorf("config: invalid allowed origin %q (expected scheme://host)", origin)
		}
	}
	return nil
}

// IsProduction reports whether production-grade defaults must be enforced.
func (c Config) IsProduction() bool { return c.Env == "production" || c.Env == "prod" }

func env(key, fallback string) string {
	if value := os.Getenv(envPrefix + key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	raw := os.Getenv(envPrefix + key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
	raw := os.Getenv(envPrefix + key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(envPrefix + key)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	return host + "-" + strconv.Itoa(os.Getpid())
}

func splitCSV(raw string) []string {
	var result []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
