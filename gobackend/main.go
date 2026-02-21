package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gobackend/compare"
	"gobackend/obs"
	"gobackend/ossstore"
	"gobackend/store"
	"gobackend/streamq"
	"gobackend/wechat"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// BillingEvent 代表一次计费事件或扣费占位
type BillingEvent struct {
	IdempotencyKey string                 `json:"idempotencyKey"`
	Amount         float64                `json:"amount"`
	ApiCall        string                 `json:"apiCall,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
	Confirmed      bool                   `json:"confirmed"`
	Deducted       bool                   `json:"deducted"`
}

type memoryStore struct {
	mu            sync.Mutex
	events        map[string]*BillingEvent
	baseBalance   float64
	totalDeducted float64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		events:      make(map[string]*BillingEvent),
		baseBalance: 100.0, // 初始虚拟余额
	}
}

func (s *memoryStore) ensurePending(key string, amount float64, apiCall string, metadata map[string]interface{}) *BillingEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ev, ok := s.events[key]; ok {
		// 已存在则直接返回，保证幂等
		return ev
	}
	ev := &BillingEvent{
		IdempotencyKey: key,
		Amount:         amount,
		ApiCall:        apiCall,
		Metadata:       metadata,
		CreatedAt:      time.Now(),
		Confirmed:      false,
		Deducted:       false,
	}
	s.events[key] = ev
	return ev
}

func (s *memoryStore) markDeducted(key string, amount float64, apiCall string) (*BillingEvent, error) {
	if key == "" {
		return nil, errors.New("idempotencyKey 不能为空")
	}
	if amount <= 0 {
		return nil, errors.New("amount 必须为正数")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ev, ok := s.events[key]
	if !ok {
		// 未提前创建也允许直接扣费，仍然记录
		ev = &BillingEvent{
			IdempotencyKey: key,
			CreatedAt:      time.Now(),
		}
		s.events[key] = ev
	}

	// 幂等：如果已经扣费过直接返回
	if ev.Deducted {
		return ev, nil
	}

	ev.Amount = amount
	if apiCall != "" {
		ev.ApiCall = apiCall
	}
	ev.Confirmed = true
	ev.Deducted = true
	s.totalDeducted += amount
	return ev, nil
}

func (s *memoryStore) balance() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return math.Max(0, s.baseBalance-s.totalDeducted)
}

var billingStore = newMemoryStore()

func main() {
	shutdownObs, _ := obs.Init("go-api")
	defer func() { _ = shutdownObs(context.Background()) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/profile", handleProfile)
	mux.HandleFunc("/billing/pending", handleCreatePending)
	mux.HandleFunc("/billing/deduct", handleDeduct)

	// Compare jobs (pay-gated export)
	tmpRoot := readEnvDefault("TMP_ROOT", "./tmp")
	redisAddr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if redisAddr == "" {
		log.Fatalf("REDIS_ADDR 为空：Streams 队列模式必须启用 Redis")
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
	}

	streamKey := readEnvDefault("COMPARE_STREAM_KEY", "gy:comparejobs:stream")
	group := readEnvDefault("COMPARE_STREAM_GROUP", "gy-compare")
	maxLen := int64(readEnvIntDefault("COMPARE_STREAM_MAXLEN", 100000))
	q := streamq.NewRedisStreamQueue(rdb, streamKey, group, maxLen)

	compareSvc := compare.NewService(jobStore, q, tmpRoot, ossSt)
	compareSvc.RegisterRoutes(mux)
	wechat.RegisterNotifyRoutes(mux, jobStore)

	addr := ":" + readEnvDefault("PORT", "8080")
	log.Printf("Go billing stub listening on %s", addr)
	// Wrap order: cors -> otel/metrics -> mux
	handler := corsMiddleware(obs.WrapHTTP("go-api", mux))
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := map[string]interface{}{
		"user_id": "demo-user",
		"balance": fmt.Sprintf("%.2f", billingStore.balance()),
	}
	writeJSON(w, http.StatusOK, resp)
}

type pendingRequest struct {
	Amount         float64                `json:"amount"`
	ApiCall        string                 `json:"apiCall,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	IdempotencyKey string                 `json:"idempotencyKey,omitempty"`
}

func handleCreatePending(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req pendingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	key := req.IdempotencyKey
	if key == "" {
		key = newKey("pending")
	}
	ev := billingStore.ensurePending(key, req.Amount, req.ApiCall, req.Metadata)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"idempotencyKey": ev.IdempotencyKey,
		"status":         "pending",
	})
}

type deductRequest struct {
	IdempotencyKey string  `json:"idempotencyKey"`
	Amount         float64 `json:"amount"`
	ApiCall        string  `json:"apiCall,omitempty"`
}

func handleDeduct(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req deductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	ev, err := billingStore.markDeducted(req.IdempotencyKey, req.Amount, req.ApiCall)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"idempotencyKey": ev.IdempotencyKey,
		"deducted":       ev.Deducted,
		"amount":         ev.Amount,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func newKey(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err == nil {
		return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
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
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

func corsMiddleware(next http.Handler) http.Handler {
	allowOrigin := readEnvDefault("CORS_ALLOW_ORIGIN", "http://localhost:5173")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
