package spam

import (
	"encoding/json"
	"strings"
	"testing"
)

// 词库全文是外部数据，只留在服务端；后台接口下发的视图不能带它。
func TestPublicHidesLexiconText(t *testing.T) {
	cfg := Config{
		Enabled: true, Words: "cnm", LexiconText: "一行敏感词\n另一行",
		LexiconName: lexiconFile, LexiconWords: 2,
	}
	raw, err := json.Marshal(cfg.Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "一行敏感词") || strings.Contains(string(raw), "lexicon_text") {
		t.Fatalf("Public 泄漏了词库全文: %s", raw)
	}
	if !cfg.Public().Installed {
		t.Fatal("有词库时 installed 应为 true")
	}
	var empty Config
	if empty.Public().Installed {
		t.Fatal("没有词库时 installed 应为 false")
	}
}

func TestNormalizeWords(t *testing.T) {
	if got := normalizeWords(" cnm \r\n法轮\r\ncnm\r\n\r\n"); got != "cnm\n法轮" {
		t.Fatalf("normalizeWords = %q", got)
	}
}
