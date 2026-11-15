package spam

import (
	"net/url"
	"strings"
	"testing"
)

// 「其它词库一律不下载」：URL 必须精确指向那一个文件，不能是仓库 tarball、
// 目录列表或 GitHub API，也不许把路径做成可配置的外部输入（防 SSRF）。
func TestLexiconURLIsSingleFile(t *testing.T) {
	got := LexiconURL()
	if !strings.HasPrefix(got,
		"https://raw.githubusercontent.com/konsheng/Sensitive-lexicon/main/") {
		t.Fatalf("词库来源不对: %s", got)
	}
	if !strings.Contains(got, url.PathEscape("微信输入法")+"/") ||
		!strings.HasSuffix(got, url.PathEscape("微信输入法内置词库26.11.7.txt")) {
		t.Fatalf("不是指定的那一份词库: %s", got)
	}
	if strings.Contains(got, "tarball") || strings.Contains(got, "api.github.com") ||
		strings.Count(got, ".txt") != 1 {
		t.Fatalf("不该下载整包或多个词库: %s", got)
	}
}

// 后台「词库位置」应该指向仓库里的目录页（不是文件、不是 API）。
func TestLexiconSourceURL(t *testing.T) {
	want := "https://github.com/konsheng/Sensitive-lexicon/tree/main/Vocabulary/" +
		url.PathEscape("微信输入法")
	if got := LexiconSourceURL(); got != want {
		t.Fatalf("词库位置 = %q, want %q", got, want)
	}
	if strings.Contains(LexiconSourceURL(), ".txt") {
		t.Fatal("词库位置应该是目录，不该指向单个文件")
	}
}
