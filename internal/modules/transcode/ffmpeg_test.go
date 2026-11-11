package transcode

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRenditionsShortVideo(t *testing.T) {
	probe := ProbeResult{DurationMS: 5000, Width: 1080, Height: 1920, FPSNum: 30, FPSDen: 1, AudioCodec: "aac"}
	renditions := BuildRenditions(probe, 180000)
	if len(renditions) != 2 {
		t.Fatalf("renditions = %d, want 2 (720p MP4 + mezzanine)", len(renditions))
	}
	if renditions[0].Format != "mp4" || renditions[0].Name != "720p" {
		t.Fatalf("first rendition = %+v", renditions[0])
	}
	if renditions[0].Width != 720 {
		t.Fatalf("short side should clamp to 720, got %d", renditions[0].Width)
	}
	if renditions[1].Format != "mezzanine" || renditions[1].AudioBitrate != 192 {
		t.Fatalf("mezzanine = %+v", renditions[1])
	}
}

func TestBuildRenditionsLongVideo(t *testing.T) {
	probe := ProbeResult{DurationMS: 600000, Width: 1920, Height: 1080, FPSNum: 60, FPSDen: 1, AudioCodec: "aac"}
	renditions := BuildRenditions(probe, 180000)
	if len(renditions) != 3 {
		t.Fatalf("renditions = %d, want 3", len(renditions))
	}
	if renditions[0].Name != "480p" || renditions[1].Name != "720p" {
		t.Fatalf("HLS tiers = %s, %s", renditions[0].Name, renditions[1].Name)
	}
	if renditions[0].FPSNum != 30 || renditions[0].FPSDen != 1 {
		t.Fatalf("480p fps should cap at 30, got %d/%d", renditions[0].FPSNum, renditions[0].FPSDen)
	}
}

func TestClampDimensionsNeverUpscales(t *testing.T) {
	w, h := clampDimensions(640, 360, 720)
	if w != 640 || h != 360 {
		t.Fatalf("upscaled unexpectedly: %dx%d", w, h)
	}
	w, h = clampDimensions(1081, 1921, 720)
	if w%2 != 0 || h%2 != 0 || w > 720 {
		t.Fatalf("invalid clamp: %dx%d", w, h)
	}
}

func TestBuildCommand(t *testing.T) {
	rendition := Rendition{
		Name: "480p", Format: "hls", Width: 480, Height: 854,
		FPSNum: 30, FPSDen: 1, MaxKbps: 1800, BufKbps: 3600,
		Level: "3.1", GOP: 120, AudioBitrate: 128, AudioChannels: 2, AudioSampleHz: 48000,
	}
	spec := BuildCommand("ffmpeg", "in/source", "out/hls/480p/index.m3u8", rendition)
	joined := strings.Join(spec.Args, " ")
	for _, expected := range []string{
		"-c:v libx264", "-profile:v high", "-level:v 3.1", "-preset medium", "-crf 23",
		"-maxrate 1800k", "-bufsize 3600k", "-pix_fmt yuv420p", "-g 120",
		"-force_key_frames expr:gte(t,n_forced*4)", "-c:a aac", "-profile:a aac_low",
		"-hls_time 4", "-hls_segment_type fmp4", "-hls_fmp4_init_filename init.mp4",
		"-f hls", "index.m3u8",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in args: %s", expected, joined)
		}
	}
	if spec.Dir == "" {
		t.Fatal("HLS command must set the working directory for init.mp4")
	}
	if strings.Contains(joined, ";") || strings.Contains(joined, "&&") {
		t.Fatal("command must never contain shell metacharacters")
	}
}

func TestRetryDelay(t *testing.T) {
	for attempt, min := range map[int]time.Duration{2: 27 * time.Second, 3: 108 * time.Second, 4: 540 * time.Second} {
		delay := RetryDelay(attempt)
		if delay < min {
			t.Fatalf("attempt %d delay %s below jittered minimum %s", attempt, delay, min)
		}
	}
	if ShouldRetry(4, false) || ShouldRetry(2, true) {
		t.Fatal("retry budget or permanent error handling is wrong")
	}
	if !ShouldRetry(2, false) {
		t.Fatal("retryable errors must be retried")
	}
}

func TestParseRationalAndSeconds(t *testing.T) {
	if num, den := parseRational("30000/1001"); num != 30000 || den != 1001 {
		t.Fatalf("rational = %d/%d", num, den)
	}
	if got := parseSecondsMS("1.5"); got != 1500 {
		t.Fatalf("seconds = %d", got)
	}
}
