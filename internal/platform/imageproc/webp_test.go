package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// gradient 生成一张易压缩的渐变测试图：纯色大图会被 WebP 压到极小，
// 不足以暴露缩放比例算错的问题，用渐变可以同时看出尺寸与内容是否失真。
func gradient(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x % 256), G: uint8(y % 256), B: uint8((x + y) % 256), A: 255,
			})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func TestMakeDisplayScalesLongEdge(t *testing.T) {
	// 3:2 的 4K 图：最长边超过 DisplayMaxEdge，必须等比缩到 2560 宽。
	original := encodePNG(t, gradient(3840, 2560))
	out, ok := MakeDisplay(original)
	if !ok {
		t.Fatal("MakeDisplay 失败")
	}
	decoded, format, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("展示图无法解码: %v", err)
	}
	if format != "webp" {
		t.Fatalf("展示图格式 = %s，期望 webp", format)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != DisplayMaxEdge {
		t.Fatalf("展示图宽 = %d，期望 %d", bounds.Dx(), DisplayMaxEdge)
	}
	if bounds.Dy() != 1707 {
		t.Fatalf("展示图高 = %d，期望 1707（等比）", bounds.Dy())
	}
	// 有损 WebP 应当明显小于同尺寸 PNG。
	if len(out) >= len(original) {
		t.Fatalf("展示图 %d 字节不小于原图 %d 字节", len(out), len(original))
	}
}

func TestMakeDisplayKeepsSmallImageSize(t *testing.T) {
	original := encodePNG(t, gradient(800, 600))
	out, ok := MakeDisplay(original)
	if !ok {
		t.Fatal("MakeDisplay 失败")
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("展示图无法解码: %v", err)
	}
	if got := decoded.Bounds().Size(); got.X != 800 || got.Y != 600 {
		t.Fatalf("小图尺寸 = %v，期望 800x600（不放大）", got)
	}
}

func TestMakeDisplayRejectsNonImage(t *testing.T) {
	if _, ok := MakeDisplay([]byte("not an image at all")); ok {
		t.Fatal("非图片输入应当返回 ok=false")
	}
}

func TestMakeAvatarFitsAndEncodes(t *testing.T) {
	original := encodePNG(t, gradient(1200, 900))
	out, ok := MakeAvatar(original)
	if !ok {
		t.Fatal("MakeAvatar 失败")
	}
	decoded, format, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("头像无法解码: %v", err)
	}
	if format != "webp" {
		t.Fatalf("头像格式 = %s，期望 webp", format)
	}
	if got := decoded.Bounds().Size(); got.X != AvatarMaxEdge || got.Y != 384 {
		t.Fatalf("头像尺寸 = %v，期望 512x384（长边 512）", got)
	}
	if len(out) >= len(original) {
		t.Fatalf("头像 %d 字节不小于原图 %d 字节", len(out), len(original))
	}
}

func TestMakeAvatarNeverUpscales(t *testing.T) {
	// 96px 的小图也要重编码成 WebP（存储里只有一种格式），但尺寸不变。
	original := encodePNG(t, gradient(96, 96))
	out, ok := MakeAvatar(original)
	if !ok {
		t.Fatal("MakeAvatar 失败")
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("头像无法解码: %v", err)
	}
	if got := decoded.Bounds().Size(); got.X != 96 || got.Y != 96 {
		t.Fatalf("小头像尺寸 = %v，期望 96x96（不放大）", got)
	}
}

func TestMakeAvatarRejectsNonImage(t *testing.T) {
	if _, ok := MakeAvatar([]byte("not an image at all")); ok {
		t.Fatal("非图片输入应当返回 ok=false")
	}
}
