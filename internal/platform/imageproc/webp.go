// Package imageproc 把用户上传的图片转成 WebP：图集的展示图、用户头像都走这里，
// 原图是否保留由调用方决定（图集保留原图，头像只留 WebP）。
package imageproc

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"

	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// DisplayMaxEdge 是图集展示图的最长边上限：超过就等比缩放，避免 4K 原图直接进列表。
	DisplayMaxEdge = 2560
	displayQuality = 82
	// AvatarMaxEdge 是头像的最长边上限：展示位最大 96px×2，512 足够视网膜屏。
	AvatarMaxEdge = 512
	avatarQuality = 85
)

// Decode 解码任意受支持的图片（png / jpeg / gif / webp）。
func Decode(data []byte) (image.Image, bool) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, false
	}
	return src, true
}

// Fit 把图片等比缩到最长边 ≤ maxEdge；已经够小就原样返回（不放大）。
func Fit(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if maxEdge <= 0 || (width <= maxEdge && height <= maxEdge) {
		return src
	}
	scale := math.Min(float64(maxEdge)/float64(width), float64(maxEdge)/float64(height))
	targetWidth := max(1, int(math.Round(float64(width)*scale)))
	targetHeight := max(1, int(math.Round(float64(height)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	return dst
}

func encodeWebP(src image.Image, quality int) ([]byte, bool) {
	var buf bytes.Buffer
	if err := webp.Encode(&buf, src, webp.Options{Quality: quality, Method: 4}); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// MakeDisplay 生成图集展示图：最长边 ≤2560 的 WebP。
// 返回 ok=false 时调用方应回退到原图展示（不能让一张坏图解不出就让上传失败）。
func MakeDisplay(data []byte) ([]byte, bool) {
	src, ok := Decode(data)
	if !ok {
		return nil, false
	}
	return encodeWebP(Fit(src, DisplayMaxEdge), displayQuality)
}

// MakeAvatar 生成头像：最长边 ≤512 的 WebP。头像只存这一份，不留原图。
func MakeAvatar(data []byte) ([]byte, bool) {
	src, ok := Decode(data)
	if !ok {
		return nil, false
	}
	return EncodeAvatar(src)
}

// EncodeAvatar 给已经解码好的图编码头像 WebP，供调用方复用同一次解码结果。
func EncodeAvatar(src image.Image) ([]byte, bool) {
	return encodeWebP(Fit(src, AvatarMaxEdge), avatarQuality)
}
