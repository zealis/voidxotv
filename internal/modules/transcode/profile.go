// Package transcode implements the Go port of the reference media pipeline semantics:
// PostgreSQL jobs are the source of truth, Redis Streams only wake workers up.
package transcode

import "math"

// ProfileVersion is recorded with every artifact manifest.
const ProfileVersion = "h264-aac-v1"

const (
	crf            = 23
	segmentSeconds = 4
)

type ProbeResult struct {
	DurationMS int64
	Width      int
	Height     int
	FPSNum     int
	FPSDen     int
	VideoCodec string
	AudioCodec string
	Container  string
}

type Rendition struct {
	Name          string
	Format        string // "mp4" | "hls" | "mezzanine"
	Width         int
	Height        int
	FPSNum        int
	FPSDen        int
	TargetKbps    int
	MaxKbps       int
	BufKbps       int
	Level         string
	GOP           int
	AudioBitrate  int
	AudioChannels int
	AudioSampleHz int
	// Codec 为空表示 H.264；"hevc" 表示可选的 H.265 流（§5.1 高级编码后置）。
	Codec string
	// CRF 为 0 时按编码器取默认值（H.264 23 / HEVC 28）。
	CRF int
}

// BuildHEVCRendition returns the optional 720p HEVC MP4. It never upscales and
// is only produced when the source short side is at least 720px; H.264 remains
// the mandatory fallback and the only HLS variant.
func BuildHEVCRendition(probe ProbeResult, shortVideoMaxMS int64) (Rendition, bool) {
	if minSide(probe.Width, probe.Height) < 720 {
		return Rendition{}, false
	}
	width, height := clampDimensions(probe.Width, probe.Height, 720)
	fpsNum, fpsDen := capFPS(probe, 30)
	// HEVC 在同质量下比 H.264 低约 30–40% 码率；峰值仍受 VBV 约束。
	return Rendition{
		Name: "720p-hevc", Format: "mp4", Width: width, Height: height,
		FPSNum: fpsNum, FPSDen: fpsDen,
		TargetKbps: 1800, MaxKbps: 2400, BufKbps: 4800,
		Level: "4.0", GOP: gopFrames(fpsNum, fpsDen),
		AudioBitrate: 128, AudioChannels: 2, AudioSampleHz: 48000,
		Codec: "hevc", CRF: 28,
	}, true
}

func minSide(width, height int) int {
	if width < height {
		return width
	}
	return height
}

// BuildRenditions mirrors the reference TranscodeProfile::for_source.
// Short videos (<= 180s) get a 720p MP4; longer ones get 480p+720p HLS.
func BuildRenditions(probe ProbeResult, shortVideoMaxMS int64) []Rendition {
	var result []Rendition
	if probe.DurationMS <= shortVideoMaxMS {
		width, height := clampDimensions(probe.Width, probe.Height, 720)
		fpsNum, fpsDen := capFPS(probe, 30)
		target, maxRate, bufsize := hlsBitrates(720)
		result = append(result, Rendition{
			Name: "720p", Format: "mp4", Width: width, Height: height,
			FPSNum: fpsNum, FPSDen: fpsDen,
			TargetKbps: target, MaxKbps: maxRate, BufKbps: bufsize,
			Level: hlsLevel(720, fpsNum, fpsDen), GOP: gopFrames(fpsNum, fpsDen),
			AudioBitrate: 128, AudioChannels: 2, AudioSampleHz: 48000,
		})
	} else {
		seen := map[[2]int]bool{}
		for _, tier := range []struct {
			height int
			name   string
			cap    int
		}{{480, "480p", 30}, {720, "720p", 60}} {
			width, height := clampDimensions(probe.Width, probe.Height, tier.height)
			key := [2]int{width, height}
			if seen[key] {
				continue
			}
			seen[key] = true
			fpsNum, fpsDen := capFPS(probe, tier.cap)
			target, maxRate, bufsize := hlsBitrates(tier.height)
			result = append(result, Rendition{
				Name: tier.name, Format: "hls", Width: width, Height: height,
				FPSNum: fpsNum, FPSDen: fpsDen,
				TargetKbps: target, MaxKbps: maxRate, BufKbps: bufsize,
				Level: hlsLevel(tier.height, fpsNum, fpsDen), GOP: gopFrames(fpsNum, fpsDen),
				AudioBitrate: 128, AudioChannels: 2, AudioSampleHz: 48000,
			})
		}
	}
	width, height := clampDimensions(probe.Width, probe.Height, 1080)
	fpsNum, fpsDen := capFPS(probe, 60)
	level := "4.0"
	if fpsValue(fpsNum, fpsDen) > 30 {
		level = "4.2"
	}
	result = append(result, Rendition{
		Name: "mezzanine", Format: "mezzanine", Width: width, Height: height,
		FPSNum: fpsNum, FPSDen: fpsDen,
		TargetKbps: 10000, MaxKbps: 12000, BufKbps: 24000,
		Level: level, GOP: gopFrames(fpsNum, fpsDen),
		AudioBitrate: 192, AudioChannels: 2, AudioSampleHz: 48000,
	})
	return result
}

func fpsValue(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func capFPS(probe ProbeResult, limit int) (int, int) {
	if fpsValue(probe.FPSNum, probe.FPSDen) > float64(limit) {
		return limit, 1
	}
	return probe.FPSNum, probe.FPSDen
}

func gopFrames(num, den int) int {
	value := int(math.Round(fpsValue(num, den) * float64(segmentSeconds)))
	if value < 1 {
		return 1
	}
	return value
}

func hlsBitrates(tier int) (int, int, int) {
	switch tier {
	case 480:
		return 1200, 1800, 3600
	case 720:
		return 2800, 3500, 7000
	default:
		return 5000, 7000, 14000
	}
}

func hlsLevel(tier, fpsNum, fpsDen int) string {
	above30 := fpsValue(fpsNum, fpsDen) > 30
	switch {
	case tier == 480:
		return "3.1"
	case tier == 720 && above30:
		return "4.0"
	case tier == 720:
		return "3.1"
	case tier == 1080 && above30:
		return "4.2"
	default:
		return "4.0"
	}
}

// clampDimensions scales by the short side, never upscales, and keeps dimensions even.
func clampDimensions(sourceWidth, sourceHeight, targetShort int) (int, int) {
	portrait := sourceWidth < sourceHeight
	short, long := sourceHeight, sourceWidth
	if portrait {
		short, long = sourceWidth, sourceHeight
	}
	if short <= targetShort {
		return evenFloor(sourceWidth), evenFloor(sourceHeight)
	}
	scaledLong := evenRound(float64(long) * float64(targetShort) / float64(short))
	if portrait {
		return targetShort, scaledLong
	}
	return scaledLong, targetShort
}

func evenFloor(value int) int {
	if value%2 == 1 {
		return value - 1
	}
	return value
}

func evenRound(value float64) int {
	rounded := int(math.Round(value))
	if rounded%2 == 1 {
		rounded++
	}
	return rounded
}
