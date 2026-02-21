package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"gobackend/domain"
)

// CompareJobStore is the shared state store for compare jobs.
//
// NOTE: Job files/results are still stored on local filesystem (TMP_ROOT). This store
// only addresses "status/paid/code_url" consistency across pods and restarts.
type CompareJobStore interface {
	Create(job *domain.CompareJob) error
	Get(id string) (*domain.CompareJob, bool, error)
	Update(id string, fn func(j *domain.CompareJob)) (*domain.CompareJob, bool, error)
}

type InMemoryCompareJobStore struct {
	mu   sync.Mutex
	jobs map[string]*domain.CompareJob
}

func NewInMemoryCompareJobStore() *InMemoryCompareJobStore {
	return &InMemoryCompareJobStore{jobs: make(map[string]*domain.CompareJob)}
}

func (s *InMemoryCompareJobStore) Create(job *domain.CompareJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
}

func (s *InMemoryCompareJobStore) Get(id string) (*domain.CompareJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j == nil {
		return nil, false, nil
	}
	// Return a copy to avoid accidental mutation/data races outside the lock.
	cp := *j
	return &cp, true, nil
}

func (s *InMemoryCompareJobStore) Update(id string, fn func(j *domain.CompareJob)) (*domain.CompareJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false, nil
	}
	fn(j)
	// Return a copy to avoid callers mutating shared state outside the lock.
	cp := *j
	return &cp, true, nil
}

type compareJobRecord struct {
	ID        string                  `json:"id"`
	Status    domain.CompareJobStatus `json:"status"`
	CreatedAt time.Time               `json:"createdAt"`

	File1Path    string `json:"file1Path"`
	File2Path    string `json:"file2Path"`
	File1OSSKey  string `json:"file1OssKey"`
	File2OSSKey  string `json:"file2OssKey"`
	File1Name    string `json:"file1Name"`
	File2Name    string `json:"file2Name"`
	ResultPath   string `json:"resultPath"`
	ResultOSSKey string `json:"resultOssKey"`

	AmountYuan  float64    `json:"amountYuan"`
	CodeURL     string     `json:"codeUrl"`
	Paid        bool       `json:"paid"`
	PaidAt      *time.Time `json:"paidAt,omitempty"`
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`

	Error string `json:"error,omitempty"`
}

func recordFromJob(j *domain.CompareJob) compareJobRecord {
	if j == nil {
		return compareJobRecord{}
	}
	return compareJobRecord{
		ID:           j.ID,
		Status:       j.Status,
		CreatedAt:    j.CreatedAt,
		File1Path:    j.File1Path,
		File2Path:    j.File2Path,
		File1OSSKey:  j.File1OSSKey,
		File2OSSKey:  j.File2OSSKey,
		File1Name:    j.File1Name,
		File2Name:    j.File2Name,
		ResultPath:   j.ResultPath,
		ResultOSSKey: j.ResultOSSKey,
		AmountYuan:   j.AmountYuan,
		CodeURL:      j.CodeURL,
		Paid:         j.Paid,
		PaidAt:       j.PaidAt,
		CancelledAt:  j.CancelledAt,
		Error:        j.Error,
	}
}

func jobFromRecord(r compareJobRecord) *domain.CompareJob {
	return &domain.CompareJob{
		ID:           r.ID,
		Status:       r.Status,
		CreatedAt:    r.CreatedAt,
		File1Path:    r.File1Path,
		File2Path:    r.File2Path,
		File1OSSKey:  r.File1OSSKey,
		File2OSSKey:  r.File2OSSKey,
		File1Name:    r.File1Name,
		File2Name:    r.File2Name,
		ResultPath:   r.ResultPath,
		ResultOSSKey: r.ResultOSSKey,
		AmountYuan:   r.AmountYuan,
		CodeURL:      r.CodeURL,
		Paid:         r.Paid,
		PaidAt:       r.PaidAt,
		CancelledAt:  r.CancelledAt,
		Error:        r.Error,
	}
}

type RedisCompareJobStore struct {
	rdb       *redis.Client
	keyPrefix string
	ttl       time.Duration
}

func readRedisDB() int {
	raw := strings.TrimSpace(os.Getenv("REDIS_DB"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func readCompareJobTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("COMPARE_JOB_TTL_SECONDS"))
	if raw == "" {
		return 7 * 24 * time.Hour
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 7 * 24 * time.Hour
	}
	return time.Duration(n) * time.Second
}

func NewRedisCompareJobStore(addr, password string) (*RedisCompareJobStore, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("REDIS_ADDR 为空")
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: strings.TrimSpace(password),
		DB:       readRedisDB(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	log.Printf("compare job store: redis enabled addr=%s db=%d ttl=%s", addr, readRedisDB(), readCompareJobTTL())

	return &RedisCompareJobStore{
		rdb:       rdb,
		keyPrefix: "gy:comparejob:",
		ttl:       readCompareJobTTL(),
	}, nil
}

func (s *RedisCompareJobStore) key(id string) string {
	return s.keyPrefix + strings.TrimSpace(id)
}

func (s *RedisCompareJobStore) Create(job *domain.CompareJob) error {
	if job == nil || strings.TrimSpace(job.ID) == "" {
		return errors.New("job/id 为空")
	}
	b, err := json.Marshal(recordFromJob(job))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.rdb.SetNX(ctx, s.key(job.ID), b, s.ttl).Err()
}

func (s *RedisCompareJobStore) Get(id string) (*domain.CompareJob, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, err := s.rdb.Get(ctx, s.key(id)).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rec compareJobRecord
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		return nil, false, err
	}
	return jobFromRecord(rec), true, nil
}

func (s *RedisCompareJobStore) Update(id string, fn func(j *domain.CompareJob)) (*domain.CompareJob, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, nil
	}
	if fn == nil {
		return nil, false, errors.New("update fn 为空")
	}

	key := s.key(id)

	var out *domain.CompareJob
	var ok bool

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	for i := 0; i < 8; i++ {
		err := s.rdb.Watch(ctx, func(tx *redis.Tx) error {
			val, err := tx.Get(ctx, key).Result()
			if err == redis.Nil {
				ok = false
				out = nil
				return nil
			}
			if err != nil {
				return err
			}
			var rec compareJobRecord
			if err := json.Unmarshal([]byte(val), &rec); err != nil {
				return err
			}
			j := jobFromRecord(rec)
			fn(j)
			out = j
			ok = true

			nb, err := json.Marshal(recordFromJob(j))
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, nb, s.ttl)
				return nil
			})
			return err
		}, key)

		if err == nil {
			return out, ok, nil
		}
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return nil, false, err
	}

	return nil, false, errors.New("redis update retry exceeded")
}
