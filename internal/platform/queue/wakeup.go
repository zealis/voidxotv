// Package queue carries best-effort transcode wakeups. PostgreSQL job rows remain the source of truth.
package queue

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"
)

const (
	StreamKey     = "yejie:jobs:transcode"
	ConsumerGroup = "yejie-workers"
	MaxLen        = int64(10000)
)

type Notifier struct {
	client *goredis.Client
}

func NewNotifier(client *goredis.Client) *Notifier { return &Notifier{client: client} }

// Notify is best-effort: a lost wakeup is harmless because the worker rescans the database.
func (n *Notifier) Notify(ctx context.Context, jobID int64) error {
	return n.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: StreamKey, MaxLen: MaxLen, Approx: true,
		Values: map[string]any{"job_id": strconv.FormatInt(jobID, 10)},
	}).Err()
}

// ReadWakeups acknowledges and returns job ids; an empty stream returns promptly.
func (n *Notifier) ReadWakeups(ctx context.Context, consumer string, count int64) ([]int64, error) {
	err := n.client.XGroupCreateMkStream(ctx, StreamKey, ConsumerGroup, "$").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, fmt.Errorf("queue: create consumer group: %w", err)
	}
	streams, err := n.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group: ConsumerGroup, Consumer: consumer,
		Streams: []string{StreamKey, ">"}, Count: count,
	}).Result()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("queue: read: %w", err)
	}
	var ids []int64
	var messageIDs []string
	for _, stream := range streams {
		for _, message := range stream.Messages {
			messageIDs = append(messageIDs, message.ID)
			if raw, ok := message.Values["job_id"].(string); ok {
				if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(messageIDs) > 0 {
		_ = n.client.XAck(ctx, StreamKey, ConsumerGroup, messageIDs...).Err()
	}
	return ids, nil
}
