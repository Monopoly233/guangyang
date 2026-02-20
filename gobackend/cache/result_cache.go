package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type ResultCache interface {
	Get(key string) (string, bool, error)
	Set(key, value string) error
}

type RedisResultCache struct {
	rdb       *redis.Client
	keyPrefix string
	ttl       time.Duration
}

func NewRedisResultCacheFromEnv() (*RedisResultCache, bool, error) {
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		return nil, false, nil
	}
	password := strings.TrimSpace(os.Getenv("REDIS_PASSWORD"))
	db := readEnvIntDefault("REDIS_DB", 0)
	ttl := readEnvDurationSecondsDefault("COMPARE_RESULT_CACHE_TTL_SECONDS", 7*24*3600)

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, true, fmt.Errorf("redis ping failed(for cache): %w", err)
	}
	return &RedisResultCache{
		rdb:       rdb,
		keyPrefix: "gy:comparecache:",
		ttl:       ttl,
	}, true, nil
}

func (c *RedisResultCache) key(k string) string {
	return c.keyPrefix + strings.TrimSpace(k)
}

func (c *RedisResultCache) Get(key string) (string, bool, error) {
	if c == nil || c.rdb == nil {
		return "", false, nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, err := c.rdb.Get(ctx, c.key(key)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return "", false, nil
	}
	return val, true, nil
}

func (c *RedisResultCache) Set(key, value string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return errors.New("cache key/value empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.rdb.Set(ctx, c.key(key), value, c.ttl).Err()
}

func readEnvIntDefault(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func readEnvDurationSecondsDefault(key string, defSeconds int64) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return time.Duration(defSeconds) * time.Second
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return time.Duration(defSeconds) * time.Second
	}
	return time.Duration(n) * time.Second
}

