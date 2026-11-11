package feed

import "testing"

func TestRankedCursorRoundTrip(t *testing.T) {
	// 必须精确往返：任何精度损失都会让分页边界行重复或漏条。
	for _, original := range []float64{0.8123456, 0.3661765, 0.000001234, 1} {
		token := formatRankedCursor(original, 42)
		score, id := parseRankedCursor(token)
		if id != 42 || score != original {
			t.Fatalf("round trip mismatch for %v: token=%q score=%v id=%d",
				original, token, score, id)
		}
	}
}

func TestRankedCursorFirstPage(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		score, id := parseRankedCursor(raw)
		if score != 0 || id != 0 {
			t.Fatalf("%q must map to the first page, got %f/%d", raw, score, id)
		}
	}
}

func TestRankedCursorRejectsMalformedInput(t *testing.T) {
	cases := []string{"abc", "0.5", "0.5:abc", "-1:3", "0.5:-2", ":"}
	for _, raw := range cases {
		score, id := parseRankedCursor(raw)
		if score != 0 || id != 0 {
			t.Fatalf("%q must fall back to the first page, got %f/%d", raw, score, id)
		}
	}
}
