package compare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gobackend/domain"
	"gobackend/excelcmp"
	"gobackend/ossstore"
	"gobackend/store"
	"gobackend/wechat"
)

type Worker struct {
	store    store.CompareJobStore
	tmpRoot  string
	oss      *ossstore.Store
	inflight chan struct{}
}

func NewWorker(st store.CompareJobStore, tmpRoot string, oss *ossstore.Store) *Worker {
	maxInflight := readEnvIntDefault("COMPARE_MAX_INFLIGHT", 4)
	if maxInflight <= 0 {
		maxInflight = 1
	}
	return &Worker{
		store:    st,
		tmpRoot:  tmpRoot,
		oss:      oss,
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

func (w *Worker) Process(jobID string) error {
	w.acquireInflight()
	defer w.releaseInflight()

	if w == nil || w.store == nil {
		return errors.New("worker/store 未初始化")
	}
	job, ok, err := w.store.Get(jobID)
	if err != nil || !ok {
		return err
	}
	if job.Status == domain.CompareJobStatusCancelled {
		return nil
	}
	if w.oss == nil || !w.oss.Enabled() {
		return w.fail(jobID, errors.New("OSS 未启用"))
	}
	if job.File1OSSKey == "" || job.File2OSSKey == "" {
		return w.fail(jobID, errors.New("输入文件 OSSKey 为空"))
	}

	jobDir := filepath.Join(w.tmpRoot, "compare_jobs", jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return w.fail(jobID, fmt.Errorf("创建 jobDir 失败: %w", err))
	}

	f1name := safeBaseNameFromName(job.File1Name)
	f2name := safeBaseNameFromName(job.File2Name)
	local1 := filepath.Join(jobDir, "file1_"+f1name)
	local2 := filepath.Join(jobDir, "file2_"+f2name)

	if err := w.oss.GetObjectToFile(job.File1OSSKey, local1); err != nil {
		return w.fail(jobID, fmt.Errorf("下载输入文件1失败: %w", err))
	}
	if err := w.oss.GetObjectToFile(job.File2OSSKey, local2); err != nil {
		return w.fail(jobID, fmt.Errorf("下载输入文件2失败: %w", err))
	}

	// .xls -> .xlsx conversion if needed
	new1, _, err := convertXLSIfNeeded(local1)
	if err != nil {
		return w.fail(jobID, err)
	}
	new2, _, err := convertXLSIfNeeded(local2)
	if err != nil {
		return w.fail(jobID, err)
	}
	local1, local2 = new1, new2

	resultPath := filepath.Join(jobDir, "comparison_result.xlsx")
	if err := excelcmp.GenerateCompareExportXLSX(local1, local2, job.File1Name, job.File2Name, resultPath); err != nil {
		return w.fail(jobID, err)
	}

	ossKey := w.oss.ObjectKeyForJob(jobID)
	if err := w.oss.PutResultFile(ossKey, resultPath); err != nil {
		return w.fail(jobID, fmt.Errorf("上传 OSS 失败: %w", err))
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
		return nil
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
		})
		return nil
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
		})
		return nil
	}

	codeURL, err := wechat.CreateNativeOrder(jobID, feeFen)
	if err != nil {
		return w.fail(jobID, fmt.Errorf("创建微信支付订单失败: %w", err))
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
	})
	return nil
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
