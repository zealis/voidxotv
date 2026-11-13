// Package avatar 封装 DiceBear Micah 随机头像的 seed 生成与 PNG 下载。
// 注册默认头像、前台「随机头像 6 选 1」、后台给用户设置头像共用这一份实现。
//
// Micah 是 Micah Lanier《Avatar Illustration System》(CC BY 4.0) 的 remix，
// 使用与署名见 README「默认头像」一节。
package avatar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	Endpoint    = "https://api.dicebear.com/10.x/micah/png"
	Size        = 256
	MaxBytes    = 256 << 10 // 256 KiB；实测 256px PNG 约 14 KiB
	FetchTTL    = 8 * time.Second
	SeedBytes   = 12
	PreviewTTL  = 24 * time.Hour
	CacheHeader = "public, max-age=86400"
)

// SeedPattern 只接受我们自己生成的 12 字节 hex seed，挡掉任意外链参数滥用预览代理。
var SeedPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

var client = &http.Client{Timeout: FetchTTL}

func RandomSeed() (string, error) {
	buf := make([]byte, SeedBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func ValidSeed(seed string) bool {
	return SeedPattern.MatchString(seed)
}

func SourceURL(seed string) string {
	return fmt.Sprintf("%s?seed=%s&size=%d", Endpoint, url.QueryEscape(seed), Size)
}

// Key 与 settings 上传头像的命名一致：删旧头像、覆盖头像都命中同一命名空间。
func Key(userID int64) string {
	return fmt.Sprintf("avatars/%d/%d.png", userID, time.Now().UnixNano())
}

// FetchPNG 下载并校验一张头像。调用方负责把它存进对象存储；本包不落盘。
func FetchPNG(ctx context.Context, seed string) ([]byte, error) {
	if !ValidSeed(seed) {
		return nil, fmt.Errorf("avatar: invalid seed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL(seed), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("avatar: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avatar: upstream status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		return nil, fmt.Errorf("avatar: unexpected content type %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("avatar: read: %w", err)
	}
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, fmt.Errorf("avatar: invalid image size %d", len(data))
	}
	return data, nil
}
