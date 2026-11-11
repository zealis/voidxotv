package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CommandSpec keeps arguments as a vector; a shell string is never used (开发技术规范 §6).
type CommandSpec struct {
	Program string
	Args    []string
	Dir     string
}

var supportedContainers = map[string]bool{
	"mov": true, "mp4": true, "m4a": true, "3gp": true, "3g2": true, "mj2": true,
	"matroska": true, "webm": true, "avi": true, "mpegts": true,
}

var supportedVideoCodecs = map[string]bool{
	"h264": true, "hevc": true, "vp9": true, "vp8": true, "av1": true, "mpeg4": true,
}

// Probe reads media metadata and rejects inputs the pipeline cannot safely transcode.
func Probe(ctx context.Context, ffprobePath, input string, maxDurationMS int64) (ProbeResult, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", input)
	output, err := cmd.Output()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe: %w", err)
	}
	var raw struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Duration  string `json:"duration"`
			AvgRate   string `json:"avg_frame_rate"`
			RRate     string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return ProbeResult{}, fmt.Errorf("parse ffprobe JSON: %w", err)
	}
	var video *struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
		AvgRate   string `json:"avg_frame_rate"`
		RRate     string `json:"r_frame_rate"`
	}
	for i := range raw.Streams {
		if raw.Streams[i].CodecType == "video" {
			video = &raw.Streams[i]
			break
		}
	}
	if video == nil {
		return ProbeResult{}, fmt.Errorf("input has no video stream")
	}
	duration := parseSecondsMS(raw.Format.Duration)
	if duration == 0 {
		duration = parseSecondsMS(video.Duration)
	}
	fpsNum, fpsDen := parseRational(video.AvgRate)
	if fpsNum == 0 {
		fpsNum, fpsDen = parseRational(video.RRate)
	}
	audioCodec := ""
	for _, stream := range raw.Streams {
		if stream.CodecType == "audio" {
			audioCodec = stream.CodecName
			break
		}
	}
	result := ProbeResult{
		DurationMS: duration, Width: video.Width, Height: video.Height,
		FPSNum: fpsNum, FPSDen: fpsDen, VideoCodec: video.CodecName,
		AudioCodec: audioCodec, Container: raw.Format.FormatName,
	}
	if err := validateProbe(result, maxDurationMS); err != nil {
		return ProbeResult{}, err
	}
	return result, nil
}

func validateProbe(probe ProbeResult, maxDurationMS int64) error {
	if probe.DurationMS <= 0 {
		return fmt.Errorf("probe found an invalid duration")
	}
	if probe.Width <= 0 || probe.Height <= 0 || probe.FPSNum <= 0 || probe.FPSDen <= 0 {
		return fmt.Errorf("probe found invalid dimensions or frame rate")
	}
	if probe.DurationMS > maxDurationMS {
		return fmt.Errorf("duration %d ms exceeds limit %d ms", probe.DurationMS, maxDurationMS)
	}
	containerOK := false
	for _, name := range strings.Split(probe.Container, ",") {
		if supportedContainers[strings.TrimSpace(name)] {
			containerOK = true
		}
	}
	if !containerOK {
		return fmt.Errorf("unsupported container %q", probe.Container)
	}
	if !supportedVideoCodecs[probe.VideoCodec] {
		return fmt.Errorf("unsupported video codec %q", probe.VideoCodec)
	}
	return nil
}

func parseSecondsMS(raw string) int64 {
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}

func parseRational(raw string) (int, int) {
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return 0, 0
	}
	num, err1 := strconv.Atoi(parts[0])
	den, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || num <= 0 || den <= 0 {
		return 0, 0
	}
	return num, den
}

// BuildCommand translates the reference FfmpegCommandBuilder into a Go argument vector.
func BuildCommand(ffmpegPath, input, output string, rendition Rendition) CommandSpec {
	args := []string{"-y", "-i", input,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-vf", fmt.Sprintf("scale=%d:%d", rendition.Width, rendition.Height),
		"-r", fmt.Sprintf("%d/%d", rendition.FPSNum, rendition.FPSDen),
	}
	// HEVC 是可选流，H.264 始终是回退（§5.1）；两者都不做上采样/升帧。
	if rendition.Codec == "hevc" {
		quality := rendition.CRF
		if quality <= 0 {
			quality = 28
		}
		args = append(args,
			"-c:v", "libx265",
			"-preset", "medium",
			"-crf", strconv.Itoa(quality),
			"-tag:v", "hvc1",
			"-x265-params", "log-level=error",
		)
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-profile:v", "high",
			"-level:v", rendition.Level,
			"-preset", "medium",
		)
		if rendition.Format != "mezzanine" {
			quality := rendition.CRF
			if quality <= 0 {
				quality = crf
			}
			args = append(args, "-crf", strconv.Itoa(quality))
		}
	}
	args = append(args,
		"-maxrate", fmt.Sprintf("%dk", rendition.MaxKbps),
		"-bufsize", fmt.Sprintf("%dk", rendition.BufKbps),
	)
	if rendition.Format == "mezzanine" {
		args = append(args, "-b:v", fmt.Sprintf("%dk", rendition.TargetKbps))
	}
	args = append(args,
		"-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(rendition.GOP),
		"-keyint_min", strconv.Itoa(rendition.GOP),
		"-sc_threshold", "0",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
	)
	if rendition.AudioBitrate > 0 {
		args = append(args,
			"-c:a", "aac", "-profile:a", "aac_low",
			"-b:a", fmt.Sprintf("%dk", rendition.AudioBitrate),
			"-ar", strconv.Itoa(rendition.AudioSampleHz),
			"-ac", strconv.Itoa(rendition.AudioChannels),
		)
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-sn")

	spec := CommandSpec{Program: ffmpegPath, Args: args}
	switch rendition.Format {
	case "hls":
		directory := filepath.Dir(output)
		args = append(args,
			"-f", "hls",
			"-hls_time", strconv.Itoa(segmentSeconds),
			"-hls_playlist_type", "vod",
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", filepath.ToSlash(filepath.Join(directory, "segment%04d.m4s")),
			"-hls_flags", "independent_segments",
			output,
		)
		spec.Args = args
		spec.Dir = directory // init.mp4 resolves relative to the working directory
	case "mp4", "mezzanine":
		args = append(args, "-movflags", "+faststart", "-f", "mp4", output)
		spec.Args = args
	}
	return spec
}

// CoverCommand produces the SEO cover and the immersive poster as WebP.
func CoverCommand(ffmpegPath, input, output string, width, height int, timestampSeconds int) CommandSpec {
	return CommandSpec{Program: ffmpegPath, Args: []string{
		"-y", "-ss", strconv.Itoa(timestampSeconds), "-i", input,
		"-vframes", "1", "-vf", fmt.Sprintf("scale=%d:%d", width, height),
		"-c:v", "libwebp", "-quality", "80", output,
	}}
}

// StoryboardCommand produces a 10x10 sprite sampled every 5 seconds.
func StoryboardCommand(ffmpegPath, input, output string) CommandSpec {
	return CommandSpec{Program: ffmpegPath, Args: []string{
		"-y", "-i", input,
		"-vf", "fps=1/5,scale=160:90,tile=10x10",
		"-frames:v", "1", "-c:v", "libwebp", "-quality", "75", output,
	}}
}

// Run executes a command with a hard timeout and bounded stderr, never via a shell.
func Run(ctx context.Context, spec CommandSpec, timeout time.Duration) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg timed out after %s", timeout)
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, stderrTail(stderr.String()))
	}
	return nil
}

func stderrTail(raw string) string {
	const limit = 400
	if len(raw) > limit {
		return raw[len(raw)-limit:]
	}
	return raw
}

// VerifyOutput re-probes an encoded file and checks duration and codecs.
// expectedVideoCodec is "h264" (default) or "hevc" for the optional rendition.
func VerifyOutput(ctx context.Context, ffprobePath, path string, expectedDurationMS int64,
	format, expectedVideoCodec string) error {

	expected := expectedVideoCodec
	if expected == "" {
		expected = "h264"
	}
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("verify probe: %w", err)
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return err
	}
	duration := parseSecondsMS(raw.Format.Duration)
	delta := duration - expectedDurationMS
	if delta < 0 {
		delta = -delta
	}
	if delta > 1000 {
		return fmt.Errorf("output duration %d ms differs from source %d ms", duration, expectedDurationMS)
	}
	videoOK, audioOK := false, false
	for _, stream := range raw.Streams {
		if stream.CodecType == "video" && stream.CodecName == expected {
			videoOK = true
		}
		if stream.CodecType == "audio" && stream.CodecName == "aac" {
			audioOK = true
		}
	}
	if !videoOK {
		label := map[string]string{"h264": "H.264", "hevc": "HEVC"}[expected]
		if label == "" {
			label = expected
		}
		return fmt.Errorf("output is missing a %s video stream", label)
	}
	if format == "hls" {
		// HLS playlists are verified by reading the local m3u8, not by probing it here.
		return nil
	}
	_ = audioOK
	return nil
}
