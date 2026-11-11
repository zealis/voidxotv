package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
)

// Purger performs targeted CDN invalidation. Failure is isolated from page
// publication (开发技术规范 §10.5): the caller logs and retries independently.
type Purger interface {
	Name() string
	Purge(ctx context.Context, urls []string) error
}

func NewPurger(cfg config.Config, log *slog.Logger) Purger {
	if cfg.CDNZoneID != "" && cfg.CDNAPIToken != "" {
		return &cloudflarePurger{zone: cfg.CDNZoneID, token: cfg.CDNAPIToken, log: log}
	}
	return &logPurger{log: log}
}

type logPurger struct {
	log *slog.Logger
}

func (p *logPurger) Name() string { return "dev-noop" }
func (p *logPurger) Purge(_ context.Context, urls []string) error {
	p.log.Info("delivery: cdn purge skipped (no CDN credentials configured)", "urls", urls)
	return nil
}

type cloudflarePurger struct {
	zone  string
	token string
	log   *slog.Logger
}

func (p *cloudflarePurger) Name() string { return "cloudflare" }
func (p *cloudflarePurger) Purge(ctx context.Context, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"files": urls})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/purge_cache", p.zone)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+p.token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 300 || !strings.Contains(string(body), `"success":true`) {
		return fmt.Errorf("delivery: cdn purge HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	p.log.Info("delivery: cdn purge ok", "urls", urls)
	return nil
}
