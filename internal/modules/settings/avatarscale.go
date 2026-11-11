// 头像上传缩放（技术规范 §11.2 账号资料）：头像在产品内最大只以 96px×2
// （视网膜）显示，原图直存会让 feed 一页拉到数 MB 的头像流量。
// 上传时把长边超过 512px 的头像等比缩到 512px——单一规范尺寸即覆盖全部
// 展示位，免去多尺寸派生与消费端改动。
//
// 口径：
//   - PNG / JPEG 解码后按 CatmullRom 重采样、按原格式重编码；
//   - WebP 标准库无法编码 → 缩放后转存为 PNG（键与 MIME 随之改变）；
//   - 解码失败（魔数通过但内容损坏等）→ 原样保存，不让上传失败；
//   - 不放大：长边 ≤512px 的原图原样保存。
package settings

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"log/slog"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // 注册 webp 解码器（image.Decode）
)

const avatarMaxSide = 512

// scaleAvatar 把头像降到 ≤avatarMaxSide 长边，返回（数据, MIME, 扩展名, 是否缩放）。
func scaleAvatar(data []byte, mime, ext string, log *slog.Logger) ([]byte, string, string, bool) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// 魔数通过但解码失败：按原样保存，不阻断上传。
		log.Warn("avatar decode failed, storing as-is", "mime", mime, "error", err)
		return data, mime, ext, false
	}
	bounds := src.Bounds()
	longSide := max(bounds.Dx(), bounds.Dy())
	if longSide <= avatarMaxSide {
		return data, mime, ext, false
	}
	// 等比缩放：长边压到 512，短边按比例取整（至少 1px）。
	scale := float64(avatarMaxSide) / float64(longSide)
	targetW := max(1, int(float64(bounds.Dx())*scale))
	targetH := max(1, int(float64(bounds.Dy())*scale))

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var out bytes.Buffer
	outMime, outExt := mime, ext
	switch mime {
	case "image/jpeg":
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 88}); err != nil {
			return data, mime, ext, false
		}
	case "image/webp":
		// 标准库不能编码 WebP → 转存为 PNG（键与 MIME 随之改变）。
		if err := png.Encode(&out, dst); err != nil {
			return data, mime, ext, false
		}
		outMime, outExt = "image/png", "png"
	default:
		if err := png.Encode(&out, dst); err != nil {
			return data, mime, ext, false
		}
	}
	return out.Bytes(), outMime, outExt, true
}
