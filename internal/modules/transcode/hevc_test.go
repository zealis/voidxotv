package transcode

import (
	"strings"
	"testing"
)

func TestBuildHEVCRenditionSkipsSmallSource(t *testing.T) {
	probe := ProbeResult{DurationMS: 10_000, Width: 360, Height: 640, FPSNum: 30, FPSDen: 1}
	if _, ok := BuildHEVCRendition(probe, 180_000); ok {
		t.Fatal("a 360x640 source must not get an HEVC rendition")
	}
}

func TestBuildHEVCRenditionCapsAt720WithoutUpscaling(t *testing.T) {
	portrait := ProbeResult{DurationMS: 30_000, Width: 1080, Height: 1920, FPSNum: 60, FPSDen: 1}
	rendition, ok := BuildHEVCRendition(portrait, 180_000)
	if !ok {
		t.Fatal("a 1080x1920 source should get an optional HEVC rendition")
	}
	if rendition.Width != 720 || rendition.Height != 1280 {
		t.Fatalf("expected 720x1280 portrait output, got %dx%d", rendition.Width, rendition.Height)
	}
	if rendition.Codec != "hevc" || rendition.CRF != 28 {
		t.Fatalf("unexpected codec settings: %+v", rendition)
	}
	// 60 fps 源对短视频仍封顶 30 fps（§5.2）。
	if fpsValue(rendition.FPSNum, rendition.FPSDen) != 30 {
		t.Fatalf("expected 30 fps cap, got %d/%d", rendition.FPSNum, rendition.FPSDen)
	}
}

func TestHEVCCommandUsesLibx265AndHvc1Tag(t *testing.T) {
	rendition, ok := BuildHEVCRendition(
		ProbeResult{DurationMS: 20_000, Width: 1080, Height: 1920, FPSNum: 30, FPSDen: 1}, 180_000)
	if !ok {
		t.Fatal("expected an HEVC rendition")
	}
	command := BuildCommand("ffmpeg", "in.mp4", "out.mp4", rendition)
	joined := strings.Join(command.Args, " ")
	for _, expected := range []string{"libx265", "-crf 28", "-tag:v hvc1", "out.mp4"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("HEVC command missing %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "libx264") {
		t.Fatal("HEVC command must not fall back to libx264")
	}
}

func TestH264CommandRemainsDefault(t *testing.T) {
	renditions := BuildRenditions(
		ProbeResult{DurationMS: 20_000, Width: 1080, Height: 1920, FPSNum: 30, FPSDen: 1}, 180_000)
	if len(renditions) == 0 {
		t.Fatal("expected at least one rendition")
	}
	command := BuildCommand("ffmpeg", "in.mp4", "out.mp4", renditions[0])
	joined := strings.Join(command.Args, " ")
	if !strings.Contains(joined, "libx264") || !strings.Contains(joined, "-crf 23") {
		t.Fatalf("H.264 command changed unexpectedly: %s", joined)
	}
	if strings.Contains(joined, "libx265") {
		t.Fatal("default rendition must stay H.264")
	}
}
