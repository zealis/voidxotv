package auth

// 新用户默认头像：注册成功后异步取一张 DiceBear Micah 随机头像，落到自有对象存储，
// 再把版本化 key 写进 users.avatar_key。与「用户自己上传头像」完全同一套读取路径
// （settings.avatarURL → store.MediaURL），运行时不依赖 dicebear.com 是否在线。
//
// Micah 风格是 Micah Lanier《Avatar Illustration System》(CC BY 4.0) 的 remix，
// 使用与署名见 README「默认头像」一节。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	dicebearEndpoint    = "https://api.dicebear.com/10.x/micah/png"
	defaultAvatarSize   = 256
	defaultAvatarTTL    = 8 * time.Second
	maxDefaultAvatarImg = 256 << 10 // 256 KiB，实测 256px PNG 约 14 KiB
)

// AvatarObjectStore 是分配默认头像所需的最小存储能力，由组装层注入 *objectstore.Store。
type AvatarObjectStore interface {
	PutBytes(ctx context.Context, key string, data []byte, contentType, cacheControl string) error
	Remove(ctx context.Context, key string) error
}

type DefaultAvatar struct {
	repo   *Repository
	store  AvatarObjectStore
	log    *slog.Logger
	client *http.Client
}

func NewDefaultAvatar(repo *Repository, store AvatarObjectStore, log *slog.Logger) *DefaultAvatar {
	return &DefaultAvatar{
		repo:   repo,
		store:  store,
		log:    log,
		client: &http.Client{Timeout: defaultAvatarTTL},
	}
}

// Assign 只由注册接口异步调用。任何失败都记 WARN 并返回，绝不影响注册结果；
// 用户没头像时前端本来就有占位图。
func (d *DefaultAvatar) Assign(ctx context.Context, userID int64, handle string) {
	if d == nil || d.repo == nil || d.store == nil {
		return
	}
	if userID <= 0 {
		return
	}

	seed, err := randomAvatarSeed()
	if err != nil {
		d.log.Warn("default avatar: seed failed", "handle", handle, "error", err)
		return
	}
	endpoint := fmt.Sprintf("%s?seed=%s&size=%d", dicebearEndpoint, url.QueryEscape(seed), defaultAvatarSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		d.log.Warn("default avatar: build request", "handle", handle, "error", err)
		return
	}
	req.Header.Set("Accept", "image/png")

	resp, err := d.client.Do(req)
	if err != nil {
		d.log.Warn("default avatar: fetch failed", "handle", handle, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.log.Warn("default avatar: unexpected status", "handle", handle, "status", resp.StatusCode)
		return
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		d.log.Warn("default avatar: unexpected content type", "handle", handle, "content_type", ct)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDefaultAvatarImg+1))
	if err != nil || len(data) == 0 || len(data) > maxDefaultAvatarImg {
		d.log.Warn("default avatar: invalid image", "handle", handle, "bytes", len(data), "error", err)
		return
	}

	key := fmt.Sprintf("avatars/%d/%d.png", userID, time.Now().UnixNano())
	if err := d.store.PutBytes(ctx, key, data, "image/png", "public, max-age=31536000, immutable"); err != nil {
		d.log.Warn("default avatar: store failed", "handle", handle, "error", err)
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
		// 用户在异步任务完成前已经自己上传了头像：不要覆盖，也不要留孤儿。
		_ = d.store.Remove(ctx, key)
		return
	}
	d.log.Info("default avatar assigned", "handle", handle, "key", key, "seed", seed)
}

func randomAvatarSeed() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
