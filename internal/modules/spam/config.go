// Package spam 是「垃圾信息控制」的服务端：后台开关与自定义词、按需下载的词库，
// 以及所有用户输入口共用的拒发校验。
//
// 配置与词库都放 system_settings：词库是整体替换的外部数据，放业务表难回滚；
// 存库而不是放文件，是为了让 API 多实例读同一份，部署不必额外挂可写卷。
package spam

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const settingPrefix = "spam."

// Config 是后台「功能开关 → 垃圾信息控制」读写的那一组配置。
// LexiconText 是词库全文（约 170 KiB），只留在服务端，绝不下发后台接口。
type Config struct {
	Enabled      bool   `json:"enabled"`
	Words        string `json:"words"`
	LexiconName  string `json:"lexicon_name"`
	LexiconText  string `json:"-"`
	LexiconWords int    `json:"lexicon_words"`
	LexiconAt    string `json:"lexicon_at"`
	// LexiconRemoteSHA 是远端 blob sha，用来先比后下；LexiconDigest 是内容 sha256，
	// 在 GitHub 接口不可达时兜底对比，避免重复下载与误报更新。
	LexiconRemoteSHA string `json:"-"`
	LexiconDigest    string `json:"-"`
	Revision         string `json:"-"`
}

// Public 是不含词库全文、可以下发给后台页面的视图。
type Public struct {
	Enabled      bool   `json:"enabled"`
	Words        string `json:"words"`
	LexiconWords int    `json:"lexicon_words"`
	LexiconAt    string `json:"lexicon_at"`
	Installed    bool   `json:"installed"`
	// 后台只显示可点击的「词库位置」，不再显示文件名。
	LexiconSource      string `json:"lexicon_source"`
	LexiconSourceLabel string `json:"lexicon_source_label"`
}

func (c Config) Public() Public {
	return Public{
		Enabled: c.Enabled, Words: c.Words,
		LexiconWords: c.LexiconWords, LexiconAt: c.LexiconAt,
		Installed:          strings.TrimSpace(c.LexiconText) != "",
		LexiconSource:      LexiconSourceURL(),
		LexiconSourceLabel: lexiconDisplayName,
	}
}

// LoadConfig 读 system_settings 里的 spam.* 键；缺键回落「关闭且无词」。
func LoadConfig(ctx context.Context, db *pgxpool.Pool) (Config, error) {
	cfg := Config{}
	rows, err := db.Query(ctx, `SELECT key, value FROM system_settings WHERE key LIKE 'spam.%'`)
	if err != nil {
		return cfg, fmt.Errorf("spam: load config: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return cfg, fmt.Errorf("spam: scan config: %w", err)
		}
		switch key {
		case "spam.enabled":
			cfg.Enabled = value == "1"
		case "spam.words":
			cfg.Words = value
		case "spam.lexicon_name":
			cfg.LexiconName = value
		case "spam.lexicon_text":
			cfg.LexiconText = value
		case "spam.lexicon_words":
			cfg.LexiconWords, _ = strconv.Atoi(value)
		case "spam.lexicon_at":
			cfg.LexiconAt = value
		case "spam.lexicon_remote_sha":
			cfg.LexiconRemoteSHA = value
		case "spam.lexicon_digest":
			cfg.LexiconDigest = value
		case "spam.revision":
			cfg.Revision = value
		}
	}
	return cfg, rows.Err()
}

// SaveConfig 保存开关与自定义词；词库相关键保持不动。
func SaveConfig(ctx context.Context, db *pgxpool.Pool, adminID int64,
	enabled bool, words string) (Config, error) {
	cfg, err := LoadConfig(ctx, db)
	if err != nil {
		return cfg, err
	}
	on := "0"
	if enabled {
		on = "1"
	}
	return cfg, savePairs(ctx, db, adminID, cfg, map[string]string{
		"spam.enabled": on,
		"spam.words":   normalizeWords(words),
	})
}

// SaveLexicon 写入下载好的词库全文，并记录词条数与时间。
func SaveLexicon(ctx context.Context, db *pgxpool.Pool, adminID int64,
	name, text string, wordCount int, at, remoteSHA, digest string) (Config, error) {
	cfg, err := LoadConfig(ctx, db)
	if err != nil {
		return cfg, err
	}
	pairs := map[string]string{
		"spam.lexicon_name":   name,
		"spam.lexicon_text":   text,
		"spam.lexicon_words":  strconv.Itoa(wordCount),
		"spam.lexicon_at":     at,
		"spam.lexicon_digest": digest,
	}
	if remoteSHA != "" {
		pairs["spam.lexicon_remote_sha"] = remoteSHA
	}
	return cfg, savePairs(ctx, db, adminID, cfg, pairs)
}

// savePairs 递增 revision 并 upsert；revision 是编译缓存的失效信号。
func savePairs(ctx context.Context, db *pgxpool.Pool, adminID int64,
	cfg Config, pairs map[string]string) error {
	current, _ := strconv.ParseInt(cfg.Revision, 10, 64)
	pairs["spam.revision"] = strconv.FormatInt(current+1, 10)
	for key, value := range pairs {
		if _, err := db.Exec(ctx, `
			INSERT INTO system_settings (key, value, updated_by, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (key) DO UPDATE SET
			  value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			key, value, adminID); err != nil {
			return fmt.Errorf("spam: save %s: %w", key, err)
		}
	}
	return nil
}

// normalizeWords 一行一个词，去空白、去重、限长，避免后台多存几 KB 空白行。
func normalizeWords(raw string) string {
	seen := make(map[string]bool)
	out := make([]string, 0, 32)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		word := strings.TrimSpace(line)
		if word == "" || len(word) > 300 || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return strings.Join(out, "\n")
}

func splitLines(raw string) []string {
	return strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
}
