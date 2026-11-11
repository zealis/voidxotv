package content

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateTypeAndText(t *testing.T) {
	if err := validateTypeAndText("audio", "t", "d"); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("audio should be rejected, got %v", err)
	}
	if err := validateTypeAndText("video", strings.Repeat("字", 31), ""); !errors.Is(err, ErrTitleTooLong) {
		t.Fatalf("long title should be rejected, got %v", err)
	}
	if err := validateTypeAndText("image", "标题", strings.Repeat("文", 201)); !errors.Is(err, ErrBodyTooLong) {
		t.Fatalf("long description should be rejected, got %v", err)
	}
	if err := validateTypeAndText("video", "标题", "说明"); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
}

func TestPublishScheduleLead(t *testing.T) {
	fixed := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	svc := &Service{now: func() time.Time { return fixed }}
	if _, err := svc.Publish(context.Background(), 1, 1, PublishInput{
		Visibility: "public", ScheduledAt: ptrTime(fixed.Add(9 * time.Minute)),
	}); !errors.Is(err, ErrScheduleSoon) {
		t.Fatalf("9-minute schedule should be rejected, got %v", err)
	}
	if _, err := svc.Publish(context.Background(), 1, 1, PublishInput{
		Visibility: "wrong",
	}); !errors.Is(err, ErrVisibility) {
		t.Fatalf("bad visibility should be rejected, got %v", err)
	}
}

func TestVisibleRules(t *testing.T) {
	svc := &Service{}
	base := Content{ID: 1, AuthorID: 7, PublishState: "published", ReviewStatus: "approved"}
	cases := []struct {
		name    string
		content Content
		viewer  int64
		want    bool
	}{
		{"public to anonymous", withVisibility(base, "public"), 0, true},
		{"private to anonymous", withVisibility(base, "private"), 0, false},
		{"private to author", withVisibility(base, "private"), 7, true},
		{"draft to author", func() Content { c := base; c.PublishState = "draft"; return c }(), 7, false},
		{"pending review", func() Content { c := base; c.ReviewStatus = "pending"; return c }(), 0, false},
		{"friends anonymous", withVisibility(base, "friends"), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := svc.Visible(context.Background(), tc.content, tc.viewer)
			if err != nil || ok != tc.want {
				t.Fatalf("visible = %v (err %v), want %v", ok, err, tc.want)
			}
		})
	}
}

func TestNormalizePOI(t *testing.T) {
	raw, err := NormalizePOI([]byte(`{"name":"外滩","address":"上海市"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "外滩") {
		t.Fatalf("poi = %s", raw)
	}
	if _, err := NormalizePOI([]byte(`not-json`)); err == nil {
		t.Fatal("invalid poi should be rejected")
	}
}

func withVisibility(content Content, visibility string) Content {
	content.Visibility = visibility
	return content
}

func ptrTime(value time.Time) *time.Time { return &value }
