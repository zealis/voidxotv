package playback

import (
	"strings"
	"testing"
)

func TestRewritePlaylistText(t *testing.T) {
	urls := map[string]string{
		"media/7/v1/hls/480p/index.m3u8":      "https://cdn.test/480p.m3u8?sig=1",
		"media/7/v1/hls/480p/init.mp4":        "https://cdn.test/init.mp4?sig=2",
		"media/7/v1/hls/480p/segment0000.m4s": "https://cdn.test/seg.m4s?sig=3",
	}
	master := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=480x854\nhls/480p/index.m3u8\n"
	got := rewritePlaylistText([]byte(master), "media/7/v1/master.m3u8", urls)
	if !strings.Contains(got, "https://cdn.test/480p.m3u8?sig=1") {
		t.Fatalf("master playlist URI was not resolved: %s", got)
	}
	if !strings.Contains(got, "#EXT-X-STREAM-INF") {
		t.Fatalf("playlist tags were dropped: %s", got)
	}

	variant := "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:3.000,\nsegment0000.m4s\n#EXT-X-ENDLIST\n"
	got = rewritePlaylistText([]byte(variant), "media/7/v1/hls/480p/index.m3u8", urls)
	if !strings.Contains(got, `URI="https://cdn.test/init.mp4?sig=2"`) {
		t.Fatalf("EXT-X-MAP URI was not resolved: %s", got)
	}
	if !strings.Contains(got, "https://cdn.test/seg.m4s?sig=3") {
		t.Fatalf("segment URI was not resolved: %s", got)
	}

	external := "#EXTM3U\nhttps://other.test/already-absolute.m3u8\n"
	if rewritePlaylistText([]byte(external), "media/7/v1/master.m3u8", urls) != external {
		t.Fatalf("absolute URLs must be left untouched")
	}
}

func TestResolveURIFallsBackToHLSPrefix(t *testing.T) {
	urls := map[string]string{
		"media/3/v1/hls/480p/index.m3u8": "https://cdn.test/variant.m3u8?sig=9",
		"media/3/v1/poster.webp":         "https://cdn.test/poster.webp",
	}
	if got := resolveURI("media/3/v1", "480p/index.m3u8", urls); got != "https://cdn.test/variant.m3u8?sig=9" {
		t.Fatalf("legacy master URI was not resolved via hls/ prefix: %q", got)
	}
	if got := resolveURI("media/3/v1", "hls/480p/index.m3u8", urls); got != "https://cdn.test/variant.m3u8?sig=9" {
		t.Fatalf("prefixed master URI was not resolved directly: %q", got)
	}
	if got := resolveURI("media/3/v1", "missing.m3u8", urls); got != "" {
		t.Fatalf("unknown URI must stay untouched, got %q", got)
	}
}

func TestKindOf(t *testing.T) {
	cases := map[string]string{
		"media/1/v1/master.m3u8":           "master",
		"media/1/v1/hls/480p/index.m3u8":   "playlist",
		"media/1/v1/hls/480p/init.mp4":     "init",
		"media/1/v1/hls/480p/segment0.m4s": "segment",
		"media/1/v1/720p.mp4":              "mp4",
		"media/1/v1/mezzanine.mp4":         "mezzanine",
		"media/1/v1/poster.webp":           "poster",
		"media/1/v1/preview.vtt":           "preview",
	}
	for key, want := range cases {
		if got := kindOf(key); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", key, got, want)
		}
	}
}
