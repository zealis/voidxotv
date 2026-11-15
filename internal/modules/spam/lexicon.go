package spam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// 词库来源固定为仓库里的那一个文件：后台点「下载 / 更新」才请求，不拉 tarball、
// 不遍历仓库、不下载其它词库。
const (
	lexiconRepo   = "konsheng/Sensitive-lexicon"
	lexiconBranch = "main"
	lexiconDir    = "Vocabulary/微信输入法"
	lexiconFile   = "微信输入法内置词库26.11.7.txt"
	lexiconPath   = lexiconDir + "/" + lexiconFile
	// 后台「词库位置」显示的文案：只给仓库名，不暴露内部目录结构。
	lexiconDisplayName = "Sensitive-lexicon"
	// 单个词库文件大小上限：上游若被改写成错误页 / 巨型文件，不该把内存打爆。
	maxLexiconBytes = 8 << 20
)

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// LexiconURL 是那份词库文件的 raw 地址（常量拼出来，不接受外部传入，防 SSRF）。
func LexiconURL() string {
	return "https://raw.githubusercontent.com/" + lexiconRepo + "/" +
		lexiconBranch + "/" + escapePath(lexiconPath)
}

// LexiconSourceURL 是后台「词库位置」链接：仓库里的目录页，方便管理员查看上游。
func LexiconSourceURL() string {
	return "https://github.com/" + lexiconRepo + "/tree/" + lexiconBranch + "/" +
		escapePath(lexiconDir)
}

// LexiconMetaURL 是 GitHub contents 接口，只用来读远端 blob sha 做「有没有更新」判断。
func LexiconMetaURL() string {
	return "https://api.github.com/repos/" + lexiconRepo + "/contents/" +
		escapePath(lexiconPath) + "?ref=" + lexiconBranch
}

// SyncResult 是下载 / 更新后给后台的概况，不含词库全文。
// Changed=false 表示远端与本地一致，本次没有覆盖词库。
type SyncResult struct {
	Name    string `json:"name"`
	Words   int    `json:"words"`
	Bytes   int    `json:"bytes"`
	At      string `json:"at"`
	Changed bool   `json:"changed"`
}

// SyncLexicon 拉取唯一的那个词库文件；先比远端 sha，相同就直接返回「已是最新」，
// 不同才下载并重建词表。GitHub API 不可达时退化为「下载后比内容哈希」。
func SyncLexicon(ctx context.Context, db *pgxpool.Pool, adminID int64) (SyncResult, error) {
	cfg, err := LoadConfig(ctx, db)
	if err != nil {
		return SyncResult{}, err
	}
	remoteSHA, _ := fetchRemoteSHA(ctx) // 失败不阻断，下面用内容哈希兜底
	if remoteSHA != "" && cfg.LexiconRemoteSHA == remoteSHA &&
		strings.TrimSpace(cfg.LexiconText) != "" {
		return SyncResult{
			Name: cfg.LexiconName, Words: cfg.LexiconWords,
			Bytes: len(cfg.LexiconText), At: cfg.LexiconAt, Changed: false,
		}, nil
	}

	body, err := fetchLexicon(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	digest := digestHex(body)
	if strings.TrimSpace(cfg.LexiconText) != "" && cfg.LexiconDigest == digest {
		// 内容没变：只补记远端 sha，方便下次直接比 sha，不动词库正文、不重建词表。
		if remoteSHA != "" && remoteSHA != cfg.LexiconRemoteSHA {
			_ = savePairs(ctx, db, adminID, cfg, map[string]string{
				"spam.lexicon_remote_sha": remoteSHA,
				"spam.lexicon_digest":     digest,
			})
		}
		return SyncResult{
			Name: cfg.LexiconName, Words: cfg.LexiconWords,
			Bytes: len(body), At: cfg.LexiconAt, Changed: false,
		}, nil
	}

	text := string(body)
	compiled := textfilter.Compile(splitLines(text))
	if compiled.Size() == 0 {
		return SyncResult{}, fmt.Errorf("词库里没有可用词条")
	}
	at := time.Now().UTC().Format(time.RFC3339)
	if _, err := SaveLexicon(ctx, db, adminID, lexiconFile, text, compiled.Size(), at,
		remoteSHA, digest); err != nil {
		return SyncResult{}, err
	}
	Reload(ctx)
	return SyncResult{
		Name: lexiconFile, Words: compiled.Size(), Bytes: len(body), At: at, Changed: true,
	}, nil
}

// fetchRemoteSHA 读 GitHub contents 接口里的 blob sha；只用于判断有没有更新。
func fetchRemoteSHA(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, LexiconMetaURL(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "voidxotv-lexicon")
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("远端版本不可用")
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.SHA), nil
}

// fetchLexicon 下载词库正文并做大小 / 编码 / 状态校验。
func fetchLexicon(ctx context.Context) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, LexiconURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("词库地址无效")
	}
	request.Header.Set("User-Agent", "voidxotv-lexicon")
	client := &http.Client{Timeout: 45 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("词库下载失败，请稍后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// 不带英文技术词：后台错误文案层会把它直接展示给管理员。
		return nil, fmt.Errorf("词库暂时无法下载，请稍后再试")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLexiconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("词库读取失败，请稍后重试")
	}
	if len(body) == 0 || len(body) > maxLexiconBytes {
		return nil, fmt.Errorf("词库文件为空或过大")
	}
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("词库编码不是 UTF-8")
	}
	return body, nil
}

func digestHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
