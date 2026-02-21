package redislock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client implements a simple Redis distributed lock: SET NX PX + Lua safe release/refresh.
// This is intended for "single job at a time" exclusion across multiple workers.
type Client struct {
	rdb    *redis.Client
	prefix string
}

func New(rdb *redis.Client, prefix string) *Client {
	return &Client{
		rdb:    rdb,
		prefix: strings.TrimSpace(prefix),
	}
}

func (c *Client) Key(jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if c == nil {
		return jobID
	}
	p := strings.TrimSpace(c.prefix)
	if p == "" {
		p = "gy:lock:comparejob:"
	}
	return p + jobID
}

func Token() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (c *Client) Acquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("redis lock 未初始化")
	}
	key = strings.TrimSpace(key)
	token = strings.TrimSpace(token)
	if key == "" || token == "" {
		return false, errors.New("lock key/token 为空")
	}
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return c.rdb.SetNX(ctx, key, token, ttl).Result()
}

var refreshScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
  return 0
end
`)

func (c *Client) Refresh(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("redis lock 未初始化")
	}
	key = strings.TrimSpace(key)
	token = strings.TrimSpace(token)
	if key == "" || token == "" {
		return false, errors.New("lock key/token 为空")
	}
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	px := ttl.Milliseconds()
	if px <= 0 {
		px = int64((2 * time.Hour).Milliseconds())
	}
	n, err := refreshScript.Run(ctx, c.rdb, []string{key}, token, px).Int64()
	if err != nil {
		return false, err
	}
	// PEXPIRE returns 1 if timeout was set, 0 otherwise.
	return n == 1, nil
}

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end
`)

func (c *Client) Release(ctx context.Context, key, token string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("redis lock 未初始化")
	}
	key = strings.TrimSpace(key)
	token = strings.TrimSpace(token)
	if key == "" || token == "" {
		return false, errors.New("lock key/token 为空")
	}
	n, err := releaseScript.Run(ctx, c.rdb, []string{key}, token).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

