package playback

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoManifest = errors.New("no published artifact manifest")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// LatestManifest returns the most recent published manifest for a content item.
func (r *Repository) LatestManifest(ctx context.Context, contentID int64) ([]byte, error) {
	var payload []byte
	err := r.db.QueryRow(ctx, `
		SELECT manifest::text FROM artifact_manifests
		WHERE content_id = $1 ORDER BY job_id DESC LIMIT 1`, contentID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoManifest
	}
	if err != nil {
		return nil, fmt.Errorf("playback: load manifest: %w", err)
	}
	return payload, nil
}
