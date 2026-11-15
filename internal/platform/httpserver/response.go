// Package httpserver defines the shared HTTP envelope and middleware chain.
package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
)

// ErrInvalidJSON is returned for oversized, malformed or unexpected JSON bodies.
var ErrInvalidJSON = errors.New("invalid JSON body")

// DecodeJSON bounds the body to 1 MiB and rejects unknown fields.
func DecodeJSON(r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidJSON
	}
	if decoder.More() {
		return ErrInvalidJSON
	}
	return nil
}

// Envelope is the single success-response shape for /api/v1.
type Envelope struct {
	Data any `json:"data"`
}

// ErrorEnvelope never leaks SQL, stack traces or internal paths.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// API 响应里可能有会话相关的 CSRF 令牌，任何一层缓存都不该留下它。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if data == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(Envelope{Data: data})
}

func Error(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorEnvelope{Error: ErrorBody{
		Code: code, Message: message, RequestID: requestID,
	}})
}
