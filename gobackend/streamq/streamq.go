package streamq

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type CompareQueue interface {
	Enqueue(ctx context.Context, jobID string) error
}

type RedisStreamQueue struct {
	rdb    *redis.Client
	stream string
	group  string
	maxLen int64
}

func NewRedisStreamQueue(rdb *redis.Client, stream, group string, maxLen int64) *RedisStreamQueue {
	if maxLen <= 0 {
		maxLen = 100000
	}
	return &RedisStreamQueue{
		rdb:    rdb,
		stream: strings.TrimSpace(stream),
		group:  strings.TrimSpace(group),
		maxLen: maxLen,
	}
}

func (q *RedisStreamQueue) Enqueue(ctx context.Context, jobID string) error {
	if q == nil || q.rdb == nil {
		return errors.New("redis stream queue 未初始化")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("jobID 为空")
	}
	stream := strings.TrimSpace(q.stream)
	if stream == "" {
		return errors.New("stream key 为空")
	}
	args := &redis.XAddArgs{
		Stream: stream,
		MaxLen: q.maxLen,
		Approx: true,
		Values: map[string]interface{}{
			"jobId": jobID,
		},
	}
	return q.rdb.XAdd(ctx, args).Err()
}

func (q *RedisStreamQueue) EnsureGroup(ctx context.Context) error {
	if q == nil || q.rdb == nil {
		return errors.New("redis stream queue 未初始化")
	}
	stream := strings.TrimSpace(q.stream)
	group := strings.TrimSpace(q.group)
	if stream == "" || group == "" {
		return errors.New("stream/group 为空")
	}
	// MKSTREAM: create stream automatically if it doesn't exist.
	err := q.rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err == nil {
		return nil
	}
	// BUSYGROUP means already exists.
	if strings.Contains(strings.ToLower(err.Error()), "busygroup") {
		return nil
	}
	return err
}

type Handler func(ctx context.Context, jobID string) error

type Consumer struct {
	rdb      *redis.Client
	stream   string
	group    string
	consumer string
	block    time.Duration
	count    int64
}

func NewConsumer(rdb *redis.Client, stream, group, consumer string) *Consumer {
	c := strings.TrimSpace(consumer)
	if c == "" {
		c = "c-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return &Consumer{
		rdb:      rdb,
		stream:   strings.TrimSpace(stream),
		group:    strings.TrimSpace(group),
		consumer: c,
		block:    10 * time.Second,
		count:    10,
	}
}

func (c *Consumer) ConsumeLoop(ctx context.Context, handler Handler) error {
	if c == nil || c.rdb == nil {
		return errors.New("consumer 未初始化")
	}
	if strings.TrimSpace(c.stream) == "" || strings.TrimSpace(c.group) == "" {
		return errors.New("stream/group 为空")
	}
	if handler == nil {
		return errors.New("handler 为空")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.consumer,
			Streams:  []string{c.stream, ">"},
			Count:    c.count,
			Block:    c.block,
			NoAck:    false,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			// transient network issue: keep looping
			log.Printf("stream consume error: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		for _, s := range res {
			for _, msg := range s.Messages {
				jobID, ok := msg.Values["jobId"]
				if !ok {
					_ = c.ack(ctx, msg.ID)
					continue
				}
				jid := fmt.Sprintf("%v", jobID)
				jid = strings.TrimSpace(jid)
				if jid == "" {
					_ = c.ack(ctx, msg.ID)
					continue
				}

				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("handler panic msg=%s jobId=%s: %v", msg.ID, jid, r)
						}
					}()
					_ = handler(ctx, jid)
				}()
				_ = c.ack(ctx, msg.ID)
			}
		}
	}
}

func (c *Consumer) ack(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return c.rdb.XAck(ctx, c.stream, c.group, id).Err()
}
