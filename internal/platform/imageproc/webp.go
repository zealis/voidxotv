// Package imageproc 把图集原图转成展示用 WebP：只用于列表 / 详情默认展示，
// 原图始终单独保留，用户点「下载原图」时才传输。
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
	// DisplayMaxEdge 是展示图最长边上限：超过就等比缩放，避免 4K 原图直接进列表。
	DisplayMaxEdge = 2560
	displayQuality = 82
)

// MakeDisplay 解码原图、按最长边缩放并编码为有损 WebP。
// 返回 ok=false 时调用方应回退到原图展示（不能让一张坏图解不出就让上传失败）。
func MakeDisplay(data []byte) ([]byte, bool) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, false
	}
	var out image.Image = src
	if width > DisplayMaxEdge || height > DisplayMaxEdge {
		scale := math.Min(float64(DisplayMaxEdge)/float64(width), float64(DisplayMaxEdge)/float64(height))
		targetWidth := int(math.Round(float64(width) * scale))
		targetHeight := int(math.Round(float64(height) * scale))
		if targetWidth < 1 {
			targetWidth = 1
		}
		if targetHeight < 1 {
			targetHeight = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
		out = dst
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, out, webp.Options{Quality: displayQuality, Method: 4}); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}
