package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

const (
	playlistCache = "max-age=30"
	segmentCache  = "max-age=2592000, immutable"
	imageCache    = "max-age=86400"
)

type ManifestFile struct {
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
}

type Manifest struct {
	JobID          int64          `json:"job_id"`
	ContentID      int64          `json:"content_id"`
	ProfileVersion string         `json:"profile_version"`
	DurationMS     int64          `json:"duration_ms"`
	Renditions     []string       `json:"renditions"`
	Files          []ManifestFile `json:"files"`
	Warnings       []string       `json:"warnings"`
	GeneratedAt    time.Time      `json:"generated_at"`
}

type Pipeline struct {
	cfg   config.Config
	jobs  *JobRepository
	store *objectstore.Store
	log   *slog.Logger
}

func NewPipeline(cfg config.Config, jobs *JobRepository, store *objectstore.Store, log *slog.Logger) *Pipeline {
	return &Pipeline{cfg: cfg, jobs: jobs, store: store, log: log}
}

// Execute downloads the source, transcodes, verifies and publishes artifacts in one job.
func (p *Pipeline) Execute(ctx context.Context, job Job, workerID string) error {
	workDir := filepath.Join(p.cfg.WorkerTempDir, fmt.Sprintf("yejie-job-%d", job.ID))
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return err
	}
	defer func() {
		// Windows may briefly hold file handles; a best-effort cleanup keeps the temp quota bounded.
		_ = os.RemoveAll(workDir)
	}()

	sourcePath := filepath.Join(workDir, "source")
	if _, err := p.store.DownloadToFile(ctx, job.ObjectKey, sourcePath, p.cfg.MaxUploadBytes); err != nil {
		return fmt.Errorf("download source: %w", err)
	}
	probe, err := Probe(ctx, p.cfg.FFprobePath, sourcePath, 4*60*60*1000)
	if err != nil {
		return fmt.Errorf("probe source: %w", err)
	}
	if err := p.jobs.SetStatus(ctx, job.ID, workerID, "processing"); err != nil {
		return err
	}

	manifest := Manifest{
		JobID: job.ID, ContentID: job.ContentID, ProfileVersion: ProfileVersion,
		DurationMS: probe.DurationMS, GeneratedAt: time.Now().UTC(),
	}
	renditions := BuildRenditions(probe, p.cfg.ShortVideoMaxMS)
	if p.cfg.TranscodeHEVC {
		if optional, ok := BuildHEVCRendition(probe, p.cfg.ShortVideoMaxMS); ok {
			renditions = append(renditions, optional)
			manifest.Warnings = append(manifest.Warnings,
				"optional HEVC rendition generated; H.264 remains the default")
		} else {
			manifest.Warnings = append(manifest.Warnings,
				"optional HEVC skipped: source short side below 720px")
		}
	}
	for _, rendition := range renditions {
		output, err := p.transcodeOne(ctx, sourcePath, workDir, rendition, probe.DurationMS)
		if err != nil {
			return fmt.Errorf("rendition %s: %w", rendition.Name, err)
		}
		manifest.Renditions = append(manifest.Renditions, rendition.Name)
		_ = output
	}

	p.generateAuxiliary(ctx, sourcePath, workDir, probe.DurationMS, &manifest)
	p.writeMasterPlaylist(workDir, renditions, &manifest)

	if err := p.jobs.SetStatus(ctx, job.ID, workerID, "verifying"); err != nil {
		return err
	}
	if err := p.publish(ctx, job, workDir, &manifest); err != nil {
		return err
	}
	if err := p.jobs.SetStatus(ctx, job.ID, workerID, "packaging"); err != nil {
		return err
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return p.jobs.MarkSucceeded(ctx, job, workerID, payload, probe.DurationMS, time.Now().UTC())
}

func (p *Pipeline) transcodeOne(ctx context.Context, sourcePath, workDir string,
	rendition Rendition, durationMS int64) (string, error) {
	var output string
	switch rendition.Format {
	case "hls":
		directory := filepath.Join(workDir, "hls", rendition.Name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", err
		}
		output = filepath.Join(directory, "index.m3u8")
	case "mezzanine":
		output = filepath.Join(workDir, "mezzanine.mp4")
	default:
		output = filepath.Join(workDir, rendition.Name+".mp4")
	}
	spec := BuildCommand(p.cfg.FFmpegPath, filepath.ToSlash(sourcePath), filepath.ToSlash(output), rendition)
	if err := Run(ctx, spec, p.cfg.TranscodeTimeout); err != nil {
		return "", err
	}
	if rendition.Format == "hls" {
		if err := verifyHLS(output); err != nil {
			return "", err
		}
		return output, nil
	}
	if err := VerifyOutput(ctx, p.cfg.FFprobePath, output, durationMS,
		rendition.Format, rendition.Codec); err != nil {
		return "", err
	}
	return output, nil
}

func verifyHLS(playlist string) error {
	content, err := os.ReadFile(playlist)
	if err != nil {
		return err
	}
	text := string(content)
	if !strings.Contains(text, "#EXTM3U") || !strings.Contains(text, "#EXT-X-TARGETDURATION") ||
		!strings.Contains(text, "init.mp4") || !strings.Contains(text, ".m4s") {
		return fmt.Errorf("HLS playlist is incomplete")
	}
	return nil
}

// generateAuxiliary creates covers, a storyboard sprite and a WebVTT preview index.
func (p *Pipeline) generateAuxiliary(ctx context.Context, sourcePath, workDir string,
	durationMS int64, manifest *Manifest) {
	coverSpec := CoverCommand(p.cfg.FFmpegPath, filepath.ToSlash(sourcePath),
		filepath.ToSlash(filepath.Join(workDir, "cover-1280x720.webp")), 1280, 720, 0)
	if err := Run(ctx, coverSpec, 2*time.Minute); err != nil {
		manifest.Warnings = append(manifest.Warnings, "cover generation failed: "+err.Error())
	} else {
		smallSpec := CoverCommand(p.cfg.FFmpegPath, filepath.ToSlash(sourcePath),
			filepath.ToSlash(filepath.Join(workDir, "cover-640x360.webp")), 640, 360, 0)
		if err := Run(ctx, smallSpec, 2*time.Minute); err != nil {
			manifest.Warnings = append(manifest.Warnings, "small cover failed: "+err.Error())
		}
		if err := os.WriteFile(filepath.Join(workDir, "poster.webp"),
			mustRead(workDir, "cover-1280x720.webp"), 0o600); err != nil {
			manifest.Warnings = append(manifest.Warnings, "poster copy failed")
		}
	}
	storyboard := filepath.Join(workDir, "storyboard.webp")
	if err := Run(ctx, StoryboardCommand(p.cfg.FFmpegPath, filepath.ToSlash(sourcePath),
		filepath.ToSlash(storyboard)), 3*time.Minute); err != nil {
		manifest.Warnings = append(manifest.Warnings, "storyboard failed: "+err.Error())
	} else {
		frames := int(durationMS / 5000)
		if frames < 1 {
			frames = 1
		}
		if frames > 100 {
			frames = 100
		}
		var vtt strings.Builder
		vtt.WriteString("WEBVTT\n\n")
		for i := 0; i < frames; i++ {
			start := i * 5
			vtt.WriteString(fmt.Sprintf("%s --> %s\nstoryboard.webp#xywh=%d,%d,160,90\n\n",
				vttTime(start), vttTime(start+5), (i%10)*160, (i/10)*90))
		}
		_ = os.WriteFile(filepath.Join(workDir, "preview.vtt"), []byte(vtt.String()), 0o600)
	}
}

func mustRead(workDir, name string) []byte {
	data, _ := os.ReadFile(filepath.Join(workDir, name))
	return data
}

func vttTime(seconds int) string {
	return fmt.Sprintf("00:%02d:%02d.000", seconds/60, seconds%60)
}

func (p *Pipeline) writeMasterPlaylist(workDir string, renditions []Rendition, manifest *Manifest) {
	var hls []Rendition
	for _, rendition := range renditions {
		if rendition.Format == "hls" {
			hls = append(hls, rendition)
		}
	}
	if len(hls) == 0 {
		return
	}
	var content strings.Builder
	content.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, rendition := range hls {
		// Variant playlists are written to workDir/hls/<name>/, so the master URI
		// must include that prefix or players resolve to a non-existent key.
		content.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.64001f,mp4a.40.2\"\nhls/%s/index.m3u8\n",
			rendition.TargetKbps*1000, rendition.Width, rendition.Height, rendition.Name))
	}
	if err := os.WriteFile(filepath.Join(workDir, "master.m3u8"), []byte(content.String()), 0o600); err != nil {
		manifest.Warnings = append(manifest.Warnings, "master playlist write failed")
	}
}

// publish uploads every generated artifact into a versioned, immutable media prefix.
func (p *Pipeline) publish(ctx context.Context, job Job, workDir string, manifest *Manifest) error {
	prefix := fmt.Sprintf("media/%d/v1", job.ContentID)
	err := filepath.WalkDir(workDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() == "source" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(workDir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		contentType, cacheControl := artifactHeaders(relative)
		key := prefix + "/" + relative
		if _, err := p.store.UploadFile(ctx, key, path, contentType, cacheControl); err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, ManifestFile{
			Key: key, ContentType: contentType, ByteSize: info.Size(),
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("publish artifacts: %w", err)
	}
	if len(manifest.Files) == 0 {
		return fmt.Errorf("no artifacts were produced")
	}
	return nil
}

func artifactHeaders(relative string) (string, string) {
	switch {
	case strings.HasSuffix(relative, ".m3u8"):
		return "application/vnd.apple.mpegurl", playlistCache
	case strings.HasSuffix(relative, ".m4s"), strings.HasSuffix(relative, "init.mp4"),
		strings.HasSuffix(relative, ".mp4"):
		return "video/mp4", segmentCache
	case strings.HasSuffix(relative, ".webp"):
		return "image/webp", imageCache
	case strings.HasSuffix(relative, ".vtt"):
		return "text/vtt; charset=utf-8", imageCache
	default:
		return "application/octet-stream", imageCache
	}
}
