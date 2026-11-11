package search

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLikePatternEscapesMetacharacters(t *testing.T) {
	cases := map[string]string{
		"夜景":         `%夜景%`,
		"100%":       `%100\%%`,
		"a_b":        `%a\_b%`,
		`back\slash`: `%back\\slash%`,
	}
	for input, want := range cases {
		if got := likePattern(input); got != want {
			t.Fatalf("likePattern(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseCriteriaValidatesQuery(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr error
	}{
		{"minimum length", "/api/v1/search?q=夜景", nil},
		{"too short", "/api/v1/search?q=夜", errQueryTooShort},
		{"blank after trim", "/api/v1/search?q=%20%20", errQueryTooShort},
		{"too long", "/api/v1/search?q=" + strings.Repeat("a", MaxQueryRunes+1), errQueryTooLong},
		{"control characters", "/api/v1/search?q=night%00city", errQueryChars},
		{"bad type", "/api/v1/search?q=night&type=audio", errBadFilter},
		{"bad duration", "/api/v1/search?q=night&duration=9", errBadFilter},
		{"duration bucket", "/api/v1/search?q=night&duration=2", nil},
	}
	for _, test := range tests {
		request := httptest.NewRequest("GET", test.target, nil)
		_, err := parseCriteria(request, 7)
		if test.wantErr == nil {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", test.name, err)
			}
			continue
		}
		if err == nil || err != test.wantErr {
			t.Fatalf("%s: got %v, want %v", test.name, err, test.wantErr)
		}
	}
}

func TestParseCriteriaCollapsesWhitespace(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/search?q=%E5%A4%9C%20%20%20%E8%A1%97", nil)
	criteria, err := parseCriteria(request, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if criteria.Query != "夜 街" {
		t.Fatalf("query not collapsed: %q", criteria.Query)
	}
	if criteria.ViewerID != 0 {
		t.Fatalf("viewer id not propagated: %d", criteria.ViewerID)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	token := formatCursor(0.8, 42)
	score, id := parseCursor(token)
	if id != 42 || score != 0.8 {
		t.Fatalf("round trip mismatch: score=%v id=%d token=%q", score, id, token)
	}
	if formatCursor(1, 7) != "1:7" {
		t.Fatalf("integer score encoding drifted: %q", formatCursor(1, 7))
	}
}

func TestCursorRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", "abc", "-1:5", "0.5:x", "0.5:-2"} {
		score, id := parseCursor(raw)
		if score != 0 || id != 0 {
			t.Fatalf("parseCursor(%q) should fall back to first page, got %v/%d", raw, score, id)
		}
	}
}

func TestDurationBounds(t *testing.T) {
	if min, max := durationBounds(1); min != 0 || max != 60_000 {
		t.Fatalf("bucket 1 = %d/%d", min, max)
	}
	if min, max := durationBounds(2); min != 60_001 || max != 180_000 {
		t.Fatalf("bucket 2 = %d/%d", min, max)
	}
	if min, max := durationBounds(3); min != 180_001 || max != 0 {
		t.Fatalf("bucket 3 = %d/%d", min, max)
	}
}
