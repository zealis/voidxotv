// Package upload implements S3 presigned multipart tickets for browser-direct uploads.
package upload

import (
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("upload not found")
	ErrInvalidInput   = errors.New("invalid upload input")
	ErrTooLarge       = errors.New("upload exceeds configured limit")
	ErrBadContentType = errors.New("unsupported content type")
	ErrBadParts       = errors.New("invalid part list")
	ErrSizeMismatch   = errors.New("stored object size does not match the ticket")
)

const maxTitleDescription = 0 // content draft is created empty; M1 fills metadata via the publish page

type PresignedPart struct {
	PartNumber int    `json:"part_number"`
	URL        string `json:"url"`
}

type Ticket struct {
	UploadJobID int64           `json:"upload_job_id"`
	ContentID   int64           `json:"content_id"`
	ObjectKey   string          `json:"object_key"`
	UploadID    string          `json:"upload_id"`
	PartSize    int64           `json:"part_size"`
	Parts       []PresignedPart `json:"parts"`
	ExpiresAt   time.Time       `json:"expires_at"`
}

type CompletedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type Status struct {
	UploadJobID     int64  `json:"upload_job_id"`
	ContentID       int64  `json:"content_id"`
	UploadStatus    string `json:"upload_status"`
	TranscodeJobID  int64  `json:"transcode_job_id,omitempty"`
	TranscodeStatus string `json:"transcode_status,omitempty"`
	LastError       string `json:"last_error,omitempty"`
}
