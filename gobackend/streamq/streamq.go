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

// Terminal marks an error as "terminal": the message should be ACKed even if err != nil.
// This matches the current product behavior: failed jobs are recorded in job store and we don't want automatic retries.
type TerminalError struct{ Err error }

func (e TerminalError) Error() string {
	if e.Err == nil {
		return "terminal"
	}
	return e.Err.Error()
}

func (e TerminalError) Unwrap() error { return e.Err }

func Terminal(err error) error { return TerminalError{Err: err} }

func IsTerminal(err error) bool {
	var te TerminalError
	return errors.As(err, &te)
}

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
	concur   chan struct{}

	// Pending handling (XAUTOCLAIM).
	claimMinIdle    time.Duration
	claimCount      int64
	claimStart      string
	claimEvery      time.Duration
	lastClaimedTime time.Time
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

		claimMinIdle: 30 * time.Second,
		claimCount:   50,
		claimStart:   "0-0",
		claimEvery:   3 * time.Second,
	}
}

// SetConcurrency sets the max concurrent handler goroutines.
// n<=1 means run sequentially.
func (c *Consumer) SetConcurrency(n int) {
	if c == nil {
		return
	}
	if n <= 1 {
		c.concur = nil
		return
	}
	c.concur = make(chan struct{}, n)
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

		// Best-effort: auto-claim pending messages (worker crash/restart).
		c.maybeAutoClaim(ctx, handler)

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
				if c.concur == nil {
					c.handleOne(ctx, handler, msg)
					continue
				}
				c.concur <- struct{}{}
				go func(m redis.XMessage) {
					defer func() { <-c.concur }()
					c.handleOne(ctx, handler, m)
				}(msg)
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

func (c *Consumer) handleOne(ctx context.Context, handler Handler, msg redis.XMessage) {
	jobID, ok := msg.Values["jobId"]
	if !ok {
		_ = c.ack(ctx, msg.ID)
		return
	}
	jid := strings.TrimSpace(fmt.Sprintf("%v", jobID))
	if jid == "" {
		_ = c.ack(ctx, msg.ID)
		return
	}

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("handler panic msg=%s jobId=%s: %v", msg.ID, jid, r)
				// treat panic as terminal to avoid hot-looping on poison message; job status should be persisted by handler.
				err = Terminal(fmt.Errorf("panic: %v", r))
			}
		}()
		err = handler(ctx, jid)
	}()

	// ACK rules:
	// - nil or Terminal(err): always ACK
	// - otherwise: keep pending (will be auto-claimed later)
	if err == nil || IsTerminal(err) {
		_ = c.ack(ctx, msg.ID)
	} else {
		log.Printf("handler non-terminal error msg=%s jobId=%s: %v (keep pending)", msg.ID, jid, err)
	}
}

func (c *Consumer) maybeAutoClaim(ctx context.Context, handler Handler) {
	if c == nil || c.rdb == nil {
		return
	}
	if c.claimEvery <= 0 || c.claimMinIdle <= 0 {
		return
	}
	now := time.Now()
	if !c.lastClaimedTime.IsZero() && now.Sub(c.lastClaimedTime) < c.claimEvery {
		return
	}
	c.lastClaimedTime = now

	// If redis doesn't support XAUTOCLAIM, it will error; we just skip (keeps backward compatibility).
	msgs, nextStart, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   c.stream,
		Group:    c.group,
		Consumer: c.consumer,
		MinIdle:  c.claimMinIdle,
		Start:    c.claimStart,
		Count:    c.claimCount,
	}).Result()
	if err != nil {
		// no spam
		if !errors.Is(err, redis.Nil) {
			log.Printf("xautoclaim error: %v", err)
		}
		return
	}
	if strings.TrimSpace(nextStart) != "" {
		c.claimStart = nextStart
	}
	for _, msg := range msgs {
		if c.concur == nil {
			c.handleOne(ctx, handler, msg)
			continue
		}
		c.concur <- struct{}{}
		go func(m redis.XMessage) {
			defer func() { <-c.concur }()
			c.handleOne(ctx, handler, m)
		}(msg)
	}
}
