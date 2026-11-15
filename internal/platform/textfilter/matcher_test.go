package textfilter

import "testing"

// 用户点名的三个例子必须同时成立：cnm 命中、cnm.com 与 cnmdcs 不命中。
// 其余用例覆盖参考插件 v1.4.3 的高级规则与本项目补上的域名 / 邮箱豁免。
func TestMatcherSmartBoundaries(t *testing.T) {
	m := Compile([]string{
		"cnm", "falun", "porn", "法轮",
		// 退化词条：不该把正常链接 / 英文单词拆碎。
		"p", "http", "https", "www", ".com", "a/b", "c@d",
		"# 注释行",
	})

	cases := []struct {
		text string
		hit  bool
	}{
		{"cnm", true},
		{"说 cnm 呢", true},
		{"CNM", true},       // 大小写不敏感
		{"cnm123", true},    // 词尾拼数字
		{"porn-site", true}, // 含分隔符逐段查
		{"法轮功", true},       // 中文子串
		{"falun666", true},  // 英文词尾数字
		{"cnmdcs", false},   // 右边界是同类字符
		{"phpcnm", false},   // 左边界是同类字符
		{"cnm.com", false},  // 域名整体豁免
		{"cnm.cn", false},
		{"www.cnm.com", false},
		{"foo.cnm.com", false},
		{"https://cnm.com/x", false},
		{"cnm@example.com", false},         // 邮箱豁免
		{"someone@cnm.com", false},         // 邮箱里的域名同样豁免
		{"https://example.com/php", false}, // p / http / .com 都被丢弃
		{"https://example.com", false},
		{"normal english text", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := m.Check(tc.text); got != tc.hit {
			t.Errorf("Check(%q) = %v, want %v (hits=%v)", tc.text, got, tc.hit, m.Hits(tc.text))
		}
	}
}

func TestMatcherHitsAreDeduplicated(t *testing.T) {
	m := Compile([]string{"cnm", "法轮", "法轮功"})
	hits := m.Hits("cnm 和 法轮功 还有 cnm")
	if len(hits) != 2 {
		t.Fatalf("hits = %v, want [cnm 法轮功]", hits)
	}
	// 长词优先：法轮功 命中后不应再重复报 法轮。
	for _, hit := range hits {
		if hit == "法轮" {
			t.Errorf("短词 法轮 不该和 法轮功 同时命中: %v", hits)
		}
	}
}

func TestCompileSkipsDegenerateEntries(t *testing.T) {
	m := Compile([]string{"", " ", "p", "http", "https", "www", ".com", "a/b", "c@d", "cnm"})
	if m.Empty() {
		t.Fatal("合法词条 cnm 应被保留")
	}
	if m.Check("php") || m.Check("https") || m.Check("example.com") {
		t.Fatal("退化词条不该命中正常英文与链接")
	}
	if !m.Check("cnm") {
		t.Fatal("合法词条 cnm 必须生效")
	}
}

func TestEmptyMatcherAllowsEverything(t *testing.T) {
	m := Compile(nil)
	if !m.Empty() || m.Check("cnm 法轮") {
		t.Fatal("空词表必须一律放行")
	}
	if got := (*Matcher)(nil).Hits("cnm"); got != nil {
		t.Fatalf("nil matcher hits = %v", got)
	}
}
