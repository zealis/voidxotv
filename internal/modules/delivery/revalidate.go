package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yejie/voidxotv/internal/platform/config"
)

// Revalidator calls the explicit internal Nuxt endpoint (开发技术规范 §10.6).
// Delivery never imports Nuxt internals and never renders HTML itself.
type Revalidator struct {
	endpoint string
	token    string
	client   *http.Client
}

func NewRevalidator(cfg config.Config) *Revalidator {
	return &Revalidator{
		endpoint: strings.TrimRight(cfg.DeliveryNuxtOrigin, "/") + "/internal/revalidate",
		token:    cfg.DeliveryToken,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Revalidator) Revalidate(ctx context.Context, paths []string) (RevalidateResult, error) {
	body, err := json.Marshal(map[string]any{"paths": paths})
	if err != nil {
		return RevalidateResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return RevalidateResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delivery-Token", r.token)

	response, err := r.client.Do(request)
	if err != nil {
		return RevalidateResult{}, fmt.Errorf("delivery: revalidate request: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return RevalidateResult{}, err
	}
	if response.StatusCode != http.StatusOK {
		return RevalidateResult{}, fmt.Errorf("delivery: revalidate HTTP %d: %s",
			response.StatusCode, strings.TrimSpace(string(payload)))
	}
	var envelope struct {
		Data RevalidateResult `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return RevalidateResult{}, fmt.Errorf("delivery: revalidate response: %w", err)
	}
	return envelope.Data, nil
}
