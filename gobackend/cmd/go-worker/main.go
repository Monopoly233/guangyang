package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"gobackend/compare"
	"gobackend/ossstore"
	"gobackend/store"
	"gobackend/streamq"
)

func main() {
	redisAddr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if redisAddr == "" {
		log.Fatalf("REDIS_ADDR 为空")
	}

	jobStore, err := store.NewRedisCompareJobStore(redisAddr, os.Getenv("REDIS_PASSWORD"))
	if err != nil {
		log.Fatalf("init redis store failed: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: strings.TrimSpace(os.Getenv("REDIS_PASSWORD")),
		DB:       readEnvIntDefault("REDIS_DB", 0),
	})

	var ossSt *ossstore.Store
	if st, enabled, err := ossstore.NewFromEnv(); err != nil {
		if enabled {
			log.Fatalf("init oss store failed: %v", err)
		}
	} else if enabled {
		ossSt = st
		log.Printf("oss store enabled bucket=%s prefix=%s", strings.TrimSpace(os.Getenv("OSS_BUCKET")), strings.TrimSpace(os.Getenv("OSS_PREFIX")))
	} else {
		log.Fatalf("OSS 未启用：worker 无法处理输入/输出文件")
	}

	streamKey := readEnvDefault("COMPARE_STREAM_KEY", "gy:comparejobs:stream")
	group := readEnvDefault("COMPARE_STREAM_GROUP", "gy-compare")
	maxLen := int64(readEnvIntDefault("COMPARE_STREAM_MAXLEN", 100000))

	q := streamq.NewRedisStreamQueue(rdb, streamKey, group, maxLen)
	ctx, cancel := signalContext()
	defer cancel()

	if err := q.EnsureGroup(ctx); err != nil {
		log.Fatalf("ensure stream group failed: %v", err)
	}

	tmpRoot := readEnvDefault("TMP_ROOT", "./tmp")
	worker := compare.NewWorker(jobStore, tmpRoot, ossSt)

	consumerName := strings.TrimSpace(os.Getenv("WORKER_CONSUMER_NAME"))
	if consumerName == "" {
		consumerName = strings.TrimSpace(os.Getenv("HOSTNAME"))
	}
	cons := streamq.NewConsumer(rdb, streamKey, group, consumerName)
	log.Printf("go-worker start stream=%s group=%s consumer=%s", streamKey, group, consumerName)

	err = cons.ConsumeLoop(ctx, func(ctx context.Context, jobID string) error {
		// handler should never crash the loop; all failures are persisted to job store.
		return worker.Process(jobID)
	})
	if err != nil && err != context.Canceled {
		log.Fatalf("consume loop exited: %v", err)
	}
}

func readEnvDefault(key, defaultVal string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}
	return val
}

func readEnvIntDefault(key string, defaultVal int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
		// second signal: hard exit
		select {
		case <-ch:
			os.Exit(1)
		case <-time.After(5 * time.Second):
		}
	}()
	return ctx, cancel
}
