package spam

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yejie/voidxotv/internal/platform/textfilter"
)

// 词库来源固定为这一个文件：后台点「下载词库」才请求，不拉 tarball、
// 不遍历仓库、不自动检测更新、不下载其它词库。
const (
	lexiconRepo   = "konsheng/Sensitive-lexicon"
	lexiconBranch = "main"
	lexiconPath   = "Vocabulary/微信输入法/微信输入法内置词库26.11.7.txt"
	lexiconName   = "微信输入法内置词库26.11.7.txt"
	// 单个词库文件大小上限：上游若被改写成错误页 / 巨型文件，不该把内存打爆。
	maxLexiconBytes = 8 << 20
)

// LexiconURL 逐段转义中文路径；它是常量拼出来的，不接受外部传入地址（防 SSRF）。
func LexiconURL() string {
	parts := strings.Split(lexiconPath, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "https://raw.githubusercontent.com/" + lexiconRepo + "/" +
		lexiconBranch + "/" + strings.Join(parts, "/")
}

// LexiconResult 是下载完成后给后台的概况，不含词库全文。
type LexiconResult struct {
	Name  string `json:"name"`
	Words int    `json:"words"`
	Bytes int    `json:"bytes"`
	At    string `json:"at"`
}

// Download 拉取唯一那个词库文件、校验后写入 system_settings，并立即重建词表。
func Download(ctx context.Context, db *pgxpool.Pool, adminID int64) (LexiconResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, LexiconURL(), nil)
	if err != nil {
		return LexiconResult{}, fmt.Errorf("词库地址无效")
	}
	request.Header.Set("User-Agent", "voidxotv-lexicon")
	client := &http.Client{Timeout: 45 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return LexiconResult{}, fmt.Errorf("词库下载失败，请稍后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// 不带英文技术词：后台错误文案层会把它直接展示给管理员。
		return LexiconResult{}, fmt.Errorf("词库暂时无法下载，请稍后再试")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLexiconBytes+1))
	if err != nil {
		return LexiconResult{}, fmt.Errorf("词库读取失败，请稍后重试")
	}
	if len(body) == 0 || len(body) > maxLexiconBytes {
		return LexiconResult{}, fmt.Errorf("词库文件为空或过大")
	}
	if !utf8.Valid(body) {
		return LexiconResult{}, fmt.Errorf("词库编码不是 UTF-8")
	}
	text := string(body)
	lines := splitLines(text)
	compiled := textfilter.Compile(lines)
	if compiled.Size() == 0 {
		return LexiconResult{}, fmt.Errorf("词库里没有可用词条")
	}
	at := time.Now().UTC().Format(time.RFC3339)
	if _, err := SaveLexicon(ctx, db, adminID, lexiconName, text, compiled.Size(), at); err != nil {
		return LexiconResult{}, err
	}
	Reload(ctx)
	return LexiconResult{
		Name: lexiconName, Words: compiled.Size(), Bytes: len(body), At: at,
	}, nil
}
