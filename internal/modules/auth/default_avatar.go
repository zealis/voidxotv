package auth

// 新用户默认头像：注册成功后异步取一张 DiceBear Critters 随机头像，落到自有对象存储，
// 再把版本化 key 写进 users.avatar_key。与「用户自己上传头像」完全同一套读取路径
// （settings.avatarURL → store.MediaURL），运行时不依赖 dicebear.com 是否在线。
//
// Critters 是 DiceBear 发布的 CC0 1.0 风格，不要求署名；来源见 README「默认头像」一节。

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/avatar"
	"github.com/yejie/voidxotv/internal/platform/imageproc"
)

// AssignTTL 供注册接口给异步任务单独设超时（不能用请求 context）。
const AssignTTL = avatar.FetchTTL + 2*time.Second

// AvatarObjectStore 是分配默认头像所需的最小存储能力，由组装层注入 *objectstore.Store。
type AvatarObjectStore interface {
	PutBytes(ctx context.Context, key string, data []byte, contentType, cacheControl string) error
	Remove(ctx context.Context, key string) error
}

type DefaultAvatar struct {
	repo  *Repository
	store AvatarObjectStore
	log   *slog.Logger
}

func NewDefaultAvatar(repo *Repository, store AvatarObjectStore, log *slog.Logger) *DefaultAvatar {
	return &DefaultAvatar{repo: repo, store: store, log: log}
}

// Assign 只由注册接口异步调用。任何失败都记 WARN 并返回，绝不影响注册结果；
// 用户没头像时前端本来就有占位图。
func (d *DefaultAvatar) Assign(ctx context.Context, userID int64, handle string) {
	if d == nil || d.repo == nil || d.store == nil || userID <= 0 {
		return
	}
	seed, err := avatar.RandomSeed()
	if err != nil {
		d.log.Warn("default avatar: seed failed", "handle", handle, "error", err)
		return
	}
	data, err := avatar.FetchPNG(ctx, seed)
	if err != nil {
		d.log.Warn("default avatar: fetch failed", "handle", handle, "error", err)
		return
	}

	// 默认头像与上传头像同口径：转成长边 ≤512 的 WebP 存一份，不留 PNG 原图。
	// DiceBear 偶发抽风转码失败时回落存 PNG（键随之改后缀），别让「没有默认头像」更糟。
	stored, mime, key := data, "image/png", strings.Replace(avatar.Key(userID), ".webp", ".png", 1)
	if webp, ok := imageproc.MakeAvatar(data); ok {
		stored, mime, key = webp, "image/webp", avatar.Key(userID)
	}
	if err := d.store.PutBytes(ctx, key, stored, mime, avatar.CacheHeader); err != nil {
		d.log.Warn("default avatar: store failed", "error", err)
		return
	}
	written, err := d.repo.SetDefaultAvatarKey(ctx, userID, key)
	if err != nil {
		// 写库失败要清掉刚写入的对象，避免留下无人引用的孤儿。
		_ = d.store.Remove(ctx, key)
		d.log.Warn("default avatar: persist failed", "handle", handle, "error", err)
		return
	}
	if !written {
		// 用户在异步任务完成前已经自己上传/选了头像：不要覆盖，也不要留孤儿。
		_ = d.store.Remove(ctx, key)
		return
	}
	d.log.Info("default avatar assigned", "handle", handle, "key", key, "seed", seed)
}
