// Package stats implements the two-level counting scheme (开发技术规范 §7.6):
// high-frequency events go to Redis first, then a 5-minute job folds the
// cumulative counters into PostgreSQL aggregate tables. No overview page ever
// aggregates raw event rows at request time.
package stats

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	playKeyPrefix    = "yejie:stats:plays:"        // bucket(5m) → cumulative plays
	contentKeyPrefix = "yejie:stats:contentplays:" // content_id → cumulative plays
	dedupKeyPrefix   = "yejie:stats:playdedup:"    // 30-minute dedup window
	dedupTTL         = 30 * time.Minute
	counterTTL       = 72 * time.Hour
)

type Counters struct{ client *goredis.Client }

func NewCounters(client *goredis.Client) *Counters { return &Counters{client: client} }

// ViewerKey identifies a viewer without storing raw PII: signed-in users use
// "u:<id>", anonymous viewers use a SHA-256 digest created by the caller.
func bucketKey(at time.Time) string {
	return strconv.FormatInt(at.UTC().Truncate(5*time.Minute).Unix(), 10)
}

// RecordPlay applies the 30-minute same-user/same-content dedup rule (§7.6).
func (c *Counters) RecordPlay(ctx context.Context, contentID int64, viewerKey string, at time.Time) (bool, error) {
	dedupBucket := at.UTC().Truncate(30 * time.Minute).Unix()
	dedupKey := fmt.Sprintf("%s%d:%s:%d", dedupKeyPrefix, contentID, viewerKey, dedupBucket)
	inserted, err := c.client.SetNX(ctx, dedupKey, 1, dedupTTL).Result()
	if err != nil {
		return false, fmt.Errorf("stats: dedup: %w", err)
	}
	if !inserted {
		return false, nil // duplicate play within the window
	}
	playKey := playKeyPrefix + bucketKey(at)
	contentKey := contentKeyPrefix + strconv.FormatInt(contentID, 10)
	pipe := c.client.Pipeline()
	pipe.Incr(ctx, playKey)
	pipe.Incr(ctx, contentKey)
	pipe.Expire(ctx, playKey, counterTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("stats: count play: %w", err)
	}
	return true, nil
}

// PlayCount returns the cumulative Redis counter for a 5-minute bucket.
// The key is intentionally NOT deleted: folding is idempotent on retries.
func (c *Counters) PlayCount(ctx context.Context, bucket time.Time) (int64, error) {
	value, err := c.client.Get(ctx, playKeyPrefix+bucketKey(bucket)).Int64()
	if err == goredis.Nil {
		return 0, nil
	}
	return value, err
}

// ContentPlayCounters returns all cumulative per-content play counters.
func (c *Counters) ContentPlayCounters(ctx context.Context) (map[int64]int64, error) {
	result := map[int64]int64{}
	var cursor uint64
	for {
		keys, next, err := c.client.Scan(ctx, cursor, contentKeyPrefix+"*", 200).Result()
		if err != nil {
			return nil, err
		}
		if len(keys) > 0 {
			values, err := c.client.MGet(ctx, keys...).Result()
			if err != nil {
				return nil, err
			}
			for i, value := range values {
				id, err := strconv.ParseInt(strings.TrimPrefix(keys[i], contentKeyPrefix), 10, 64)
				if err != nil {
					continue
				}
				count, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
				if err == nil {
					result[id] = count
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return result, nil
}
