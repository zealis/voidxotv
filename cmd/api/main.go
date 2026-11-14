// Command api starts the 夜街 modular monolith HTTP service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/yejie/voidxotv/internal/platform/httpserver"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yejie/voidxotv/internal/app"
	"github.com/yejie/voidxotv/internal/modules/admin"
	"github.com/yejie/voidxotv/internal/modules/auth"
	"github.com/yejie/voidxotv/internal/modules/content"
	"github.com/yejie/voidxotv/internal/modules/delivery"
	"github.com/yejie/voidxotv/internal/modules/feed"
	"github.com/yejie/voidxotv/internal/modules/gifs"
	"github.com/yejie/voidxotv/internal/modules/interactions"
	"github.com/yejie/voidxotv/internal/modules/notifications"
	"github.com/yejie/voidxotv/internal/modules/playback"
	"github.com/yejie/voidxotv/internal/modules/search"
	"github.com/yejie/voidxotv/internal/modules/seo"
	"github.com/yejie/voidxotv/internal/modules/settings"
	"github.com/yejie/voidxotv/internal/modules/stats"
	"github.com/yejie/voidxotv/internal/modules/upload"
	"github.com/yejie/voidxotv/internal/platform/buildinfo"
	"github.com/yejie/voidxotv/internal/platform/cache"
	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/database"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
	"github.com/yejie/voidxotv/internal/platform/queue"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	cacheStore, err := cache.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer cacheStore.Close()

	// Modules are assembled here; they never construct their own connections.
	authRepository := auth.NewRepository(db)
	sessionStore := auth.NewSessionStore(cacheStore, cfg)
	loginLimiter := auth.NewLoginLimiter(cacheStore.Client(), 10, 15*time.Minute)
	authService := auth.NewService(authRepository, sessionStore, loginLimiter, cfg, log)
	authHandler := auth.NewHandler(authService, cfg, log)
	// 找回密码（§11.7）：验证码 / 频控 / 图形验证码状态存 Redis。
	authHandler.SetVerificationStore(auth.NewVerificationStore(cacheStore.Client()))
	contentService := content.NewService(content.NewRepository(db))
	contentHandler := content.NewHandler(contentService, authHandler, log)
	deliveryHandler := delivery.NewHandler(delivery.NewRepository(db), cfg, log)

	// M4：后台账号体系与普通账号分离，但共用同一个 PostgreSQL/Redis 连接池。
	admin.SetTrustedImportHosts(cfg.TrustedImportHosts)
	adminRepo := admin.NewRepository(db)
	adminSessions := admin.NewSessionStore(cacheStore.Client(), cfg)
	aggregator := stats.NewAggregator(db, cacheStore.Client(), log)
	var posterResolver admin.StoreResolver
	adminHandler := admin.NewHandler(adminRepo, adminSessions, cfg, log, nil, aggregator)
	interactionsHandler := interactions.NewHandler(db,
		stats.NewCounters(cacheStore.Client()), authHandler, adminRepo, log)
	notificationsHandler := notifications.NewHandler(db, authHandler, log)
	// GIF 表情包：服务端代理上游图源（密钥不出站），搜索按人限流并落库缓存。
	gifsHandler := gifs.NewHandler(db, authHandler, cacheStore.Client(), log)
	// M11：用户侧设置页（账号 / 隐私 / 通知 / 播放 / 通用），通知偏好由此生效。
	settingsHandler := settings.NewHandler(settings.NewRepository(db), authHandler, log)
	// 改密后吊销全部会话（§11.4）：设置页需要能触达会话存储。
	settingsHandler.SetSessions(sessionStore)

	// S3 is optional at boot: the API stays available for health/auth/content even if the bucket is down.
	var uploadHandler *upload.Handler
	var playbackHandler *playback.Handler
	var feedHandler *feed.Handler
	var seoHandler *seo.Handler
	var searchHandler *search.Handler
	if store, err := objectstore.New(cfg); err == nil {
		posterResolver = store
		adminHandler = admin.NewHandler(adminRepo, adminSessions, cfg, log,
			posterResolver, aggregator)
		if err := store.EnsureBucket(ctx); err != nil {
			log.Warn("bucket check failed", "error", err)
		}
		// 作者侧图集上传复用同一个对象存储（§8.1）。S3 不可用时 content 模块
		// 仍可启动，只是图集上传返回 503。
		contentHandler.SetImageStore(store, cfg.S3Bucket)
		// 头像复用同一个对象存储；未注入时头像接口返回 503（§11.2 账号资料）。
		settingsHandler.SetImageStore(store)
		// 后台「给用户设置随机头像」复用 settings 的写入逻辑（同一套对象存储与删旧头像规则）。
		adminHandler.SetAvatarSetter(settingsHandler.SetRandomAvatar)
		// 新用户注册后异步取一张 DiceBear Critters 随机头像并存入同一个桶；
		// S3 不可用时保持 nil，注册流程不受影响，前端回落占位头像。
		authHandler.SetDefaultAvatar(auth.NewDefaultAvatar(authRepository, store, log))
		// 通知列表的触发者头像复用同一存储；未注入时回退到分组图标，不影响列表。
		notificationsHandler.SetImageStore(store)
		// 个人库卡片封面（图集首图 / 视频 poster）复用同一存储；未注入时回退类型占位。
		interactionsHandler.SetImageStore(store)
		notifier := queue.NewNotifier(cacheStore.Client())
		uploadHandler = upload.NewHandler(upload.NewRepository(db), store, notifier, authHandler, cfg, log)
		playbackHandler = playback.NewHandler(playback.NewRepository(db), store, contentService,
			authHandler, cfg.S3PublicRead, log)
		feedHandler = feed.NewHandler(feed.NewRepository(db), playbackHandler, store,
			contentService, authHandler, cfg.S3PublicRead,
			cfg.FeedRanking, cfg.FeedCandidateDays, log)
		seoHandler = seo.NewHandler(seo.NewRepository(db, store, cfg.SiteURL), log)
		// 搜索复用 feed 的条目组装，保证结果卡与沉浸流字段一致。
		searchHandler = search.NewHandler(search.NewRepository(db), feedHandler,
			authHandler, cacheStore.Client(), cfg.S3PublicRead, log)
	} else {
		log.Warn("object storage disabled", "error", err)
	}

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: app.NewRouter(app.Dependencies{
			Cfg: cfg, Log: log, DB: db, Metrics: httpserver.NewMetrics(), Cache: cacheStore,
			Auth: authHandler, Content: contentHandler, Upload: uploadHandler,
			Playback: playbackHandler, Feed: feedHandler, Delivery: deliveryHandler,
			SEO: seoHandler, Admin: adminHandler, Interactions: interactionsHandler,
			Search: searchHandler, Notifications: notificationsHandler,
			Settings: settingsHandler, Gifs: gifsHandler,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", "error", err)
		}
	}()

	log.Info("api listening", "addr", cfg.HTTPAddr, "env", cfg.Env, "version", buildinfo.Version, "commit", buildinfo.Commit)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func parseLevel(raw string) slog.Level {
	switch raw {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
