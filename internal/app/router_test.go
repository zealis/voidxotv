package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/httpserver"
)

func testDependencies() Dependencies {
	return Dependencies{
		Cfg: config.Config{AllowedOrigins: []string{"http://127.0.0.1:3000"}, APIOrigin: "http://127.0.0.1:8080"},
		Log: slog.New(slog.NewTextHandler(os.NewFile(0, os.DevNull), nil)),
	}
}

func TestLive(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewRouter(testDependencies()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("live status = %d, want 200", recorder.Code)
	}
	if recorder.Header().Get("X-Request-Id") == "" {
		t.Fatal("request id header missing")
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("CSP header missing")
	}
	var live struct {
		Data struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&live); err != nil {
		t.Fatalf("decode live body: %v", err)
	}
	if live.Data.Version == "" {
		t.Fatal("live body should expose version")
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewRouter(testDependencies()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	var payload httpserver.ErrorEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "not_found" {
		t.Fatalf("code = %q", payload.Error.Code)
	}
}

func TestReadyWithoutDependencies(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewRouter(testDependencies()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/feed", nil)
	request.Header.Set("Origin", "http://127.0.0.1:3000")
	recorder := httptest.NewRecorder()
	NewRouter(testDependencies()).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:3000" {
		t.Fatal("allowed origin missing")
	}
}
