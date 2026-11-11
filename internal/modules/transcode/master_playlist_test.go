package transcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The master playlist must point into hls/<name>/, matching publish()'s key layout.
func TestMasterPlaylistUsesHLSPrefix(t *testing.T) {
	dir := t.TempDir()
	manifest := &Manifest{}
	renditions := []Rendition{
		{Name: "480p", Format: "hls", Width: 360, Height: 640, TargetKbps: 1200},
		{Name: "720p", Format: "hls", Width: 360, Height: 640, TargetKbps: 2800},
	}
	(&Pipeline{}).writeMasterPlaylist(dir, renditions, manifest)

	data, err := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	if err != nil {
		t.Fatalf("master playlist was not written: %v", err)
	}
	text := string(data)
	for _, want := range []string{"#EXTM3U", "hls/480p/index.m3u8", "hls/720p/index.m3u8"} {
		if !strings.Contains(text, want) {
			t.Fatalf("master playlist missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\n480p/index.m3u8") || strings.Contains(text, "\n720p/index.m3u8") {
		t.Fatalf("master playlist still uses the old non-prefixed URI:\n%s", text)
	}
	if len(manifest.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", manifest.Warnings)
	}
}

func TestMasterPlaylistSkippedWithoutHLS(t *testing.T) {
	dir := t.TempDir()
	(&Pipeline{}).writeMasterPlaylist(dir, []Rendition{{Name: "720p", Format: "mp4"}}, &Manifest{})
	if _, err := os.Stat(filepath.Join(dir, "master.m3u8")); !os.IsNotExist(err) {
		t.Fatalf("MP4-only jobs must not emit a master playlist, got err=%v", err)
	}
}
