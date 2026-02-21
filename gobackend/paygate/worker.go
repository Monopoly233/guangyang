package paygate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gobackend/domain"
	"gobackend/redislock"
	"gobackend/store"
	"gobackend/streamq"
	"gobackend/wechat"
)

type Worker struct {
	store    store.CompareJobStore
	lock     *redislock.Client
	lockTTL  time.Duration
	lockKick time.Duration
}

func NewWorker(st store.CompareJobStore, lock *redislock.Client) *Worker {
	lockTTL := readEnvDurationSecondsDefault("COMPARE_PAYGATE_LOCK_TTL_SECONDS", 15*time.Minute)
	lockKick := readEnvDurationSecondsDefault("COMPARE_PAYGATE_LOCK_REFRESH_SECONDS", 10*time.Second)
	if lockKick <= 0 {
		lockKick = 10 * time.Second
	}
	return &Worker{
		store:    st,
		lock:     lock,
		lockTTL:  lockTTL,
		lockKick: lockKick,
	}
}

func (w *Worker) Process(ctx context.Context, jobID string) error {
	if w == nil || w.store == nil {
		return errors.New("paygate worker/store 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return streamq.Terminal(errors.New("jobID 为空"))
	}

	// Distributed lock: prevent duplicate payment-gate processing across replicas.
	if w.lock != nil {
		token, err := redislock.Token()
		if err != nil {
			return err
		}
		lockKey := w.lock.Key("paygate:" + jobID)
		ok, err := w.lock.Acquire(ctx, lockKey, token, w.lockTTL)
		if err != nil {
			return err
		}
		if !ok {
			return streamq.Terminal(fmt.Errorf("paygate locked: %s", lockKey))
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
					_, _ = w.lock.Refresh(context.Background(), lockKey, token, w.lockTTL)
				}
			}
		}()
	}

	job, ok, err := w.store.Get(jobID)
	if err != nil {
		return err
	}
	if !ok || job == nil {
		return streamq.Terminal(nil)
	}
	if job.Status == domain.CompareJobStatusCancelled || job.Status == domain.CompareJobStatusReady || job.Status == domain.CompareJobStatusFailed {
		return streamq.Terminal(nil)
	}

	ossKey := strings.TrimSpace(job.ResultOSSKey)
	if ossKey == "" {
		// compare stage not finished yet (or failed to persist). Keep pending, will be auto-claimed later.
		return errors.New("result not ready (ResultOSSKey empty)")
	}

	// If payment already confirmed, release.
	if job.Paid {
		_, _, _ = w.store.Update(jobID, func(j *domain.CompareJob) {
			if j.Status == domain.CompareJobStatusCancelled {
				return
			}
			j.Status = domain.CompareJobStatusReady
			j.ResultOSSKey = ossKey
			j.ResultPath = ""
			j.AmountYuan = 0
			j.CodeURL = ""
			j.Error = ""
		})
		return streamq.Terminal(nil)
	}

	feeFen := FeeFen()
	if feeFen <= 0 {
		now := time.Now()
		_, _, _ = w.store.Update(jobID, func(j *domain.CompareJob) {
			if j.Status == domain.CompareJobStatusCancelled {
				return
			}
			if !j.Paid {
				j.Paid = true
				j.PaidAt = &now
			}
			j.Status = domain.CompareJobStatusReady
			j.ResultOSSKey = ossKey
			j.ResultPath = ""
			j.AmountYuan = 0
			j.CodeURL = ""
			j.Error = ""
		})
		return streamq.Terminal(nil)
	}

	// If already awaiting payment and has code_url, don't create another order.
	if job.Status == domain.CompareJobStatusAwaitingPayment && strings.TrimSpace(job.CodeURL) != "" {
		return streamq.Terminal(nil)
	}

	codeURL, err := wechat.CreateNativeOrder(jobID, feeFen)
	if err != nil {
		// Business failure: mark job failed and ACK (no auto retry).
		return streamq.Terminal(w.fail(jobID, fmt.Errorf("创建微信支付订单失败: %w", err)))
	}
	_, _, _ = w.store.Update(jobID, func(j *domain.CompareJob) {
		if j.Status == domain.CompareJobStatusCancelled || j.Paid {
			return
		}
		j.Status = domain.CompareJobStatusAwaitingPayment
		j.ResultOSSKey = ossKey
		j.ResultPath = ""
		j.AmountYuan = float64(feeFen) / 100.0
		j.CodeURL = codeURL
		j.Error = ""
	})
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

