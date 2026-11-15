// Package textfilter 编译敏感词表并做「中文子串 + 纯 ASCII 按词」的智能匹配。
//
// 参考 owlsgo sensitive-words v1.4.3，并补上域名 / URL / 邮箱豁免：
//
//	· 中文没有词边界，词条走子串匹配（最长优先），"法轮" 能命中 "法轮功"；
//	· 纯 ASCII 词条必须整段成词（左右不再是同类字符），所以 cnm 命中 cnm、
//	  不命中 cnmdcs / phpcnm；大小写不敏感，词尾拼数字（cnm123）仍命中；
//	· 看着像域名的 token（cnm.com / www.cnm.com / cnm@example.com）整段豁免，
//	  不再逐段把 cnm 打出来——域名是不是垃圾由域名名单管，不靠拆词误伤；
//	· 单字符、http/https/www、含 / 或 @、以 . 开头的退化词条直接丢弃，
//	  否则用户粘贴的链接会被拆碎。
package textfilter

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxWordBytes = 300

// 协议 / 主机前缀不是违禁词，当词条收录只会打碎正常链接。
var asciiIgnore = map[string]bool{"http": true, "https": true, "www": true}

type subWord struct {
	word    string
	display string
}

// Matcher 是编译后的只读词表，可被多个请求并发使用。
type Matcher struct {
	subByFirst map[byte][]subWord
	latin      map[string]string // 小写词条 -> 原始写法（后台测试里显示原词）
	count      int
}

// Compile 把一行一词的词条列表编译成词表。坏行（空 / 注释 / 超长 / 非 UTF-8 /
// 控制字符）一律跳过，不让一条坏数据废掉整份词库。
func Compile(lines []string) *Matcher {
	m := &Matcher{
		subByFirst: make(map[byte][]subWord),
		latin:      make(map[string]string),
	}
	seenSub := make(map[string]bool)
	for _, raw := range lines {
		word := strings.TrimSpace(raw)
		if word == "" || strings.HasPrefix(word, "#") || len(word) > maxWordBytes {
			continue
		}
		if !utf8.ValidString(word) || hasControl(word) {
			continue
		}
		if isPureASCII(word) {
			if len(word) < 2 { // 单字符词条会打碎每一个含该字母的单词
				continue
			}
			lower := strings.ToLower(word)
			if asciiIgnore[lower] || strings.ContainsAny(word, "/@") ||
				strings.HasPrefix(word, ".") || looksLikeDomain(word) {
				continue // 域名词条匹配时整段豁免，收进来只会虚增词条数
			}
			if !strings.Contains(word, " ") && isTokenWord(word) {
				if _, exists := m.latin[lower]; !exists {
					m.latin[lower] = word
					m.count++
				}
				continue
			}
		}
		// 中文词条 / ASCII 短语（含空格）：字面量子串匹配。
		if seenSub[word] {
			continue
		}
		seenSub[word] = true
		first := word[0]
		m.subByFirst[first] = append(m.subByFirst[first], subWord{word: word, display: word})
		m.count++
	}
	// 同一位置优先最长匹配：strtr 的语义，避免短词先命中后长词再也匹配不到。
	for first := range m.subByFirst {
		words := m.subByFirst[first]
		sort.Slice(words, func(i, j int) bool { return len(words[i].word) > len(words[j].word) })
	}
	return m
}

// Empty 表示词表里没有任何可用词条（未启用 / 未下载词库时一律放行）。
func (m *Matcher) Empty() bool { return m == nil || m.count == 0 }

// Size 返回编译后的词条数，供后台显示词库概况。
func (m *Matcher) Size() int {
	if m == nil {
		return 0
	}
	return m.count
}

// Hits 返回文本里命中的原始词条（去重、保持发现顺序）。没有命中返回 nil。
// 拒发只需要判断有没有命中；后台测试框用返回值告诉管理员命中了哪几条。
func (m *Matcher) Hits(text string) []string {
	if m.Empty() || text == "" {
		return nil
	}
	var hits []string
	seen := make(map[string]bool)
	add := func(word string) {
		if !seen[word] {
			seen[word] = true
			hits = append(hits, word)
		}
	}

	// ① 中文 / 短语：按字节逐位置查首字节分桶，命中后跳过整条词。
	for i := 0; i < len(text); {
		matched := ""
		for _, candidate := range m.subByFirst[text[i]] {
			if strings.HasPrefix(text[i:], candidate.word) {
				matched = candidate.display
				i += len(candidate.word)
				break
			}
		}
		if matched != "" {
			add(matched)
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		if size <= 0 {
			break
		}
		i += size
	}

	// ② 纯 ASCII：一整段词字符就是一个 token，天然带左右词边界。
	for i := 0; i < len(text); {
		if !isWordByte(text[i]) {
			i++
			continue
		}
		start := i
		for i < len(text) && isWordByte(text[i]) {
			i++
		}
		token := text[start:i]

		// 邮箱的本地部分 / 域名部分都不按敏感词判：cnm@example.com 不该因 cnm 被拒。
		if (start > 0 && text[start-1] == '@') || (i < len(text) && text[i] == '@') {
			continue
		}
		// 域名 / URL 主机整体豁免，不逐段拆词。
		if looksLikeDomain(token) {
			continue
		}

		lower := strings.ToLower(token)
		if word, ok := m.latin[lower]; ok {
			add(word)
			continue
		}
		// 词尾拼数字绕过：cnm123 -> cnm。
		bare := strings.TrimRight(lower, "0123456789")
		if bare != "" && bare != lower {
			if word, ok := m.latin[bare]; ok {
				add(word)
				continue
			}
		}
		// 含分隔符的写法（porn-site / cnm_com）：逐段查，只认命中的段。
		if strings.ContainsAny(token, "._-'") {
			for _, piece := range strings.FieldsFunc(token, func(r rune) bool {
				return r == '.' || r == '_' || r == '-' || r == '\''
			}) {
				if word, ok := m.latin[strings.ToLower(piece)]; ok {
					add(word)
				}
			}
		}
	}
	return hits
}

// Check 只关心是否命中，比构造 Hits 切片更省。
func (m *Matcher) Check(text string) bool { return len(m.Hits(text)) > 0 }

func isPureASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// isWordByte / isTokenWord 必须与「词字符」的定义一致：字母、数字和
// . _ - ' 都算词内字符，所以 cnm.com 是一个 token、cnmdcs 也是一个 token。
func isWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' ||
		b == '.' || b == '_' || b == '-' || b == '\''
}

func isTokenWord(s string) bool {
	if s == "" || !isWordByte(s[0]) || s[0] == '.' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return false
		}
	}
	return true
}

// looksLikeDomain 判断一个 token 是不是域名 / URL 主机。
// 末段是 2–24 位纯字母（com / cn / xyz / top …）且至少两段即豁免；
// IP 之类纯数字末段不豁免，避免把 a.123 当成域名放过。
func looksLikeDomain(token string) bool {
	if strings.HasPrefix(strings.ToLower(token), "www.") {
		return true
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return false
	}
	last := parts[len(parts)-1]
	if len(last) < 2 || len(last) > 24 {
		return false
	}
	for i := 0; i < len(last); i++ {
		b := last[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z') {
			return false
		}
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}
