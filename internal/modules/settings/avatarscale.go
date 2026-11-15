// 头像一律转成 WebP 保存，且只存这一份（不留原图）。
//
// 口径（技术规范 §11.2 账号资料）：
//   - 头像在产品内最大只以 96px×2（视网膜）显示，统一压到长边 ≤512 的 WebP；
//   - 不放大：长边本来就不足 512 的也照样重编码，保证存储里只有一种格式；
//   - 解码失败（魔数通过但内容损坏）→ 拒收，避免把打不开的字节写进对象存储。
package settings

import (
	"fmt"

	"github.com/yejie/voidxotv/internal/platform/imageproc"
)

// toWebPAvatar 把任意受支持格式的头像转成 WebP，返回（数据, 是否缩小了尺寸）。
// 解码一次复用到底：先判尺寸变化，再用同一份解码结果编码。
func toWebPAvatar(data []byte) ([]byte, bool, error) {
	src, ok := imageproc.Decode(data)
	if !ok {
		return nil, false, fmt.Errorf("%w：图片内容无法识别", ErrBadAvatar)
	}
	bounds := src.Bounds()
	resized := bounds.Dx() > imageproc.AvatarMaxEdge || bounds.Dy() > imageproc.AvatarMaxEdge
	out, ok := imageproc.EncodeAvatar(src)
	if !ok {
		return nil, false, fmt.Errorf("%w：头像转码失败", ErrBadAvatar)
	}
	return out, resized, nil
}
