package interactions

import (
	"strings"
	"testing"
)

// 140 字的口径：标点与单个表情各算 1，GIF 标记整体算 1（用户看到的是一张图）。
func TestCommentLength(t *testing.T) {
	gif := "[[gif:https://api.klipy.com/x.gif]]"
	cases := []struct {
		name, body string
		want       int
	}{
		{"中文与全角标点各算一个", "你好，世界！", 6},
		{"表情各算一个", "😀😀😀", 3},
		{"文字加 GIF", strings.Repeat("a", 139) + " " + gif, 140},
		{"只有 GIF", gif, 1},
		{"超限", strings.Repeat("a", 141), 141},
	}
	for _, tc := range cases {
		if got := commentLength(tc.body); got != tc.want {
			t.Errorf("%s: commentLength(%q) = %d, want %d", tc.name, tc.body, got, tc.want)
		}
	}
}
