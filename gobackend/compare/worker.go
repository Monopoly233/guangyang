package compare

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gobackend/domain"
	"gobackend/excelcmp"
	"gobackend/ossstore"
	"gobackend/redislock"
	"gobackend/store"
	"gobackend/streamq"
)

type Worker struct {
	store    store.CompareJobStore
	tmpRoot  string
	oss      *ossstore.Store
	payq     streamq.CompareQueue
	lock     *redislock.Client
	lockTTL  time.Duration
	lockKick time.Duration
	inflight chan struct{}
}

func NewWorker(st store.CompareJobStore, tmpRoot string, oss *ossstore.Store, payq streamq.CompareQueue, lock *redislock.Client) *Worker {
	maxInflight := readEnvIntDefault("COMPARE_MAX_INFLIGHT", 4)
	if maxInflight <= 0 {
		maxInflight = 1
	}
	lockTTL := readEnvDurationSecondsDefault("COMPARE_JOB_LOCK_TTL_SECONDS", 2*time.Hour)
	lockKick := readEnvDurationSecondsDefault("COMPARE_JOB_LOCK_REFRESH_SECONDS", 30*time.Second)
	if lockKick <= 0 {
		lockKick = 30 * time.Second
	}
	return &Worker{
		store:    st,
		tmpRoot:  tmpRoot,
		oss:      oss,
		payq:     payq,
		lock:     lock,
		lockTTL:  lockTTL,
		lockKick: lockKick,
		inflight: make(chan struct{}, maxInflight),
	}
}

func (w *Worker) acquireInflight() {
	if w == nil || w.inflight == nil {
		return
	}
	w.inflight <- struct{}{}
}

func (w *Worker) releaseInflight() {
	if w == nil || w.inflight == nil {
		return
	}
	select {
	case <-w.inflight:
	default:
	}
}

func (w *Worker) Process(ctx context.Context, jobID string) error {
	w.acquireInflight()
	defer w.releaseInflight()

	if w == nil || w.store == nil {
		return errors.New("worker/store 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Distributed lock: prevent duplicate processing across multiple compare-worker replicas.
	if w.lock != nil {
		token, err := redislock.Token()
		if err != nil {
			return err
		}
		lockKey := w.lock.Key(jobID)
		ok, err := w.lock.Acquire(ctx, lockKey, token, w.lockTTL)
		if err != nil {
			// transient: keep pending
			return err
		}
		if !ok {
			// Likely a duplicate enqueue; ACK and move on.
			return streamq.Terminal(fmt.Errorf("job locked: %s", lockKey))
		}
		defer func() {
			_, _ = w.lock.Release(context.Background(), lockKey, token)
		}()

		stopKick := make(chan struct{})
		defer close(stopKick)
		go func() {
			t := time.NewTicker(w.lockKick)
			defer t.Stop()
			for {
				select {
				case <-stopKick:
					return
				case <-ctx.Done():
					return
				case <-t.C:
					_, err := w.lock.Refresh(context.Background(), lockKey, token, w.lockTTL)
					if err != nil {
						// best-effort; TTL is long enough for typical jobs
						log.Printf("lock refresh failed job=%s: %v", jobID, err)
					}
				}
			}
		}()
	}

	job, ok, err := w.store.Get(jobID)
	if err != nil || !ok {
		return err
	}
	if job.Status == domain.CompareJobStatusCancelled {
		return streamq.Terminal(nil)
	}
	if job.Status == domain.CompareJobStatusReady || job.Status == domain.CompareJobStatusFailed {
		return streamq.Terminal(nil)
	}
	// If result already exists, only enqueue pay-gate stage (idempotent).
	if strings.TrimSpace(job.ResultOSSKey) != "" && (job.Status == domain.CompareJobStatusAwaitingPayment || job.Status == domain.CompareJobStatusProcessing) {
		if w.payq == nil {
			return streamq.Terminal(w.fail(jobID, errors.New("paygate queue 未初始化")))
		}
		enqueueCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := w.payq.Enqueue(enqueueCtx, jobID); err != nil {
			// keep pending to retry enqueue (no re-compare due to ResultOSSKey short-circuit)
			return err
		}
		return streamq.Terminal(nil)
	}
	if w.oss == nil || !w.oss.Enabled() {
		return streamq.Terminal(w.fail(jobID, errors.New("OSS 未启用")))
	}
	if job.File1OSSKey == "" || job.File2OSSKey == "" {
		return streamq.Terminal(w.fail(jobID, errors.New("输入文件 OSSKey 为空")))
	}

	// Mark as processing (best-effort).
	_, _, _ = w.store.Update(jobID, func(j *domain.CompareJob) {
		if j.Status == domain.CompareJobStatusCancelled {
			return
		}
		j.Status = domain.CompareJobStatusProcessing
		j.Error = ""
	})

	jobDir := filepath.Join(w.tmpRoot, "compare_jobs", jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return streamq.Terminal(w.fail(jobID, fmt.Errorf("创建 jobDir 失败: %w", err)))
	}

	f1name := safeBaseNameFromName(job.File1Name)
	f2name := safeBaseNameFromName(job.File2Name)
	local1 := filepath.Join(jobDir, "file1_"+f1name)
	local2 := filepath.Join(jobDir, "file2_"+f2name)

	if err := w.oss.GetObjectToFile(job.File1OSSKey, local1); err != nil {
		return streamq.Terminal(w.fail(jobID, fmt.Errorf("下载输入文件1失败: %w", err)))
	}
	if err := w.oss.GetObjectToFile(job.File2OSSKey, local2); err != nil {
		return streamq.Terminal(w.fail(jobID, fmt.Errorf("下载输入文件2失败: %w", err)))
	}

	// .xls -> .xlsx conversion if needed
	new1, _, err := convertXLSIfNeeded(local1)
	if err != nil {
		return streamq.Terminal(w.fail(jobID, err))
	}
	new2, _, err := convertXLSIfNeeded(local2)
	if err != nil {
		return streamq.Terminal(w.fail(jobID, err))
	}
	local1, local2 = new1, new2

	resultPath := filepath.Join(jobDir, "comparison_result.xlsx")
	if err := excelcmp.GenerateCompareExportXLSX(local1, local2, job.File1Name, job.File2Name, resultPath); err != nil {
		return streamq.Terminal(w.fail(jobID, err))
	}

	ossKey := w.oss.ObjectKeyForJob(jobID)
	if err := w.oss.PutResultFile(ossKey, resultPath); err != nil {
		return streamq.Terminal(w.fail(jobID, fmt.Errorf("上传 OSS 失败: %w", err)))
	}
	_ = os.Remove(resultPath)

	// Persist result location early.
	_, _, _ = w.store.Update(jobID, func(j *domain.CompareJob) {
		if j.Status == domain.CompareJobStatusCancelled {
			return
		}
		j.ResultOSSKey = ossKey
		j.ResultPath = ""
	})

	// Refresh job state after generating result (Paid/Cancelled may change concurrently).
	job, ok, err = w.store.Get(jobID)
	if err != nil || !ok {
		return err
	}
	if job.Status == domain.CompareJobStatusCancelled {
		return streamq.Terminal(nil)
	}
	if w.payq == nil {
		return streamq.Terminal(w.fail(jobID, errors.New("paygate queue 未初始化")))
	}
	enqueueCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := w.payq.Enqueue(enqueueCtx, jobID); err != nil {
		// keep pending: will retry enqueue only (ResultOSSKey exists)
		return err
	}
	return streamq.Terminal(nil)
}

func (w *Worker) fail(jobID string, err error) error {
	if strings.TrimSpace(jobID) == "" {
		return err
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_, _, _ = w.store.Update(jobID, func(j *domain.CompareJob) {
		j.Status = domain.CompareJobStatusFailed
		j.Error = msg
	})
	return err
}

func readEnvDurationSecondsDefault(key string, defaultVal time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return time.Duration(n) * time.Second
}
