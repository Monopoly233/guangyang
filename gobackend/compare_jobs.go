package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CompareJobStatus string

const (
	CompareJobStatusProcessing      CompareJobStatus = "processing"
	CompareJobStatusAwaitingPayment CompareJobStatus = "awaiting_payment"
	CompareJobStatusReady           CompareJobStatus = "ready"
	CompareJobStatusFailed          CompareJobStatus = "failed"
	CompareJobStatusCancelled       CompareJobStatus = "cancelled"
)

type CompareJob struct {
	ID        string           `json:"jobId"`
	Status    CompareJobStatus `json:"status"`
	CreatedAt time.Time        `json:"createdAt"`

	// Inputs (saved on disk)
	File1Path string `json:"-"`
	File2Path string `json:"-"`

	// Result (saved on disk)
	ResultPath string `json:"-"`

	// Payment gating
	AmountYuan  float64    `json:"amount,omitempty"` // 单位：元（AwaitingPayment 时返回给前端展示）
	CodeURL     string     `json:"code_url,omitempty"`
	Paid        bool       `json:"paid"`
	PaidAt      *time.Time `json:"paidAt,omitempty"`
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`

	// Diagnostics (non-sensitive)
	Error string `json:"error,omitempty"`
}

type InMemoryCompareJobStore struct {
	mu   sync.Mutex
	jobs map[string]*CompareJob
}

func newInMemoryCompareJobStore() *InMemoryCompareJobStore {
	return &InMemoryCompareJobStore{jobs: make(map[string]*CompareJob)}
}

func (s *InMemoryCompareJobStore) Create(job *CompareJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
}

func (s *InMemoryCompareJobStore) Get(id string) (*CompareJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok, nil
}

func (s *InMemoryCompareJobStore) Update(id string, fn func(j *CompareJob)) (*CompareJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false, nil
	}
	fn(j)
	return j, true, nil
}

type CompareService struct {
	store     CompareJobStore
	queue     *InMemoryQueue
	tmpRoot   string
	pyAPIBase string
}

func newCompareService(store CompareJobStore, queue *InMemoryQueue, tmpRoot, pyAPIBase string) *CompareService {
	return &CompareService{
		store:     store,
		queue:     queue,
		tmpRoot:   tmpRoot,
		pyAPIBase: strings.TrimRight(pyAPIBase, "/"),
	}
}

// compareJobFeeFen returns fee in "fen" (1 yuan = 100 fen).
// Default is 0 (free) to allow "支付 0 元" without creating a WeChat order.
// You can override by setting env COMPARE_JOB_FEE_FEN (non-negative integer).
func compareJobFeeFen() int64 {
	raw := strings.TrimSpace(os.Getenv("COMPARE_JOB_FEE_FEN"))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (s *CompareService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/compare/jobs", s.handleCreateJob)
	mux.HandleFunc("/compare/jobs/", s.handleJobRoutes)
}

func (s *CompareService) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart
	if err := r.ParseMultipartForm(64 << 20); err != nil { // 64MB
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	f1, f1h, err := r.FormFile("file1")
	if err != nil {
		http.Error(w, "missing file1", http.StatusBadRequest)
		return
	}
	defer f1.Close()
	f2, f2h, err := r.FormFile("file2")
	if err != nil {
		http.Error(w, "missing file2", http.StatusBadRequest)
		return
	}
	defer f2.Close()

	jobID := newJobID()
	jobDir := filepath.Join(s.tmpRoot, "compare_jobs", jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		http.Error(w, "failed to create job dir", http.StatusInternalServerError)
		return
	}

	file1Path, err := saveUploadTo(jobDir, "file1_"+safeBaseName(f1h), f1)
	if err != nil {
		http.Error(w, "failed to save file1", http.StatusInternalServerError)
		return
	}
	file2Path, err := saveUploadTo(jobDir, "file2_"+safeBaseName(f2h), f2)
	if err != nil {
		http.Error(w, "failed to save file2", http.StatusInternalServerError)
		return
	}

	job := &CompareJob{
		ID:        jobID,
		Status:    CompareJobStatusProcessing,
		CreatedAt: time.Now(),
		File1Path: file1Path,
		File2Path: file2Path,
		Paid:      false,
	}
	_ = s.store.Create(job)

	// Enqueue background compare (actual implementation in worker)
	if s.queue != nil {
		s.queue.Enqueue(func() {
			s.runCompareTask(jobID)
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobId":  jobID,
		"status": string(job.Status),
	})
}

func (s *CompareService) handleJobRoutes(w http.ResponseWriter, r *http.Request) {
	// /compare/jobs/{jobId}
	// /compare/jobs/{jobId}/export
	// /compare/jobs/{jobId}/cancel
	path := strings.TrimPrefix(r.URL.Path, "/compare/jobs/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.Error(w, "jobId required", http.StatusBadRequest)
		return
	}
	parts := strings.Split(path, "/")
	jobID := parts[0]
	if jobID == "" {
		http.Error(w, "jobId required", http.StatusBadRequest)
		return
	}

	if len(parts) == 1 {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleGetJob(w, r, jobID)
		return
	}

	if len(parts) == 2 && parts[1] == "export" {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleDownloadExport(w, r, jobID)
		return
	}

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleCancelJob(w, r, jobID)
		return
	}

	http.NotFound(w, r)
}

func (s *CompareService) handleGetJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok, err := s.store.Get(jobID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	// 防御性：若已支付且结果已生成，但状态仍停留在 awaiting_payment，则对外视为 ready
	// （避免轮询端一直弹支付框）。
	status := job.Status
	if status == CompareJobStatusAwaitingPayment && job.Paid && job.ResultPath != "" {
		status = CompareJobStatusReady
	}
	// Return a safe subset
	resp := map[string]interface{}{
		"jobId":     job.ID,
		"status":    string(status),
		"createdAt": job.CreatedAt,
		"paid":      job.Paid,
	}
	if status == CompareJobStatusAwaitingPayment {
		resp["amount"] = job.AmountYuan
		resp["code_url"] = job.CodeURL
	}
	if job.Status == CompareJobStatusFailed && job.Error != "" {
		resp["error"] = job.Error
	}
	if job.CancelledAt != nil {
		resp["cancelledAt"] = job.CancelledAt
	}
	if job.PaidAt != nil {
		resp["paidAt"] = job.PaidAt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *CompareService) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok, err := s.store.Get(jobID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Idempotent: already cancelled.
	if job.Status == CompareJobStatusCancelled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"jobId":     job.ID,
			"status":    string(job.Status),
			"cancelled": true,
		})
		return
	}

	// If already paid/released, don't allow cancel.
	if job.Paid || job.Status == CompareJobStatusReady {
		http.Error(w, "订单已支付或已放行，无法取消", http.StatusConflict)
		return
	}

	// If we already created a WeChat order, attempt to close it first.
	if job.Status == CompareJobStatusAwaitingPayment {
		if err := closeWechatNativeOrder(jobID); err != nil {
			http.Error(w, "关闭微信订单失败: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	now := time.Now()
	_, _, _ = s.store.Update(jobID, func(j *CompareJob) {
		// Don't overwrite if paid concurrently.
		if j.Paid {
			return
		}
		j.Status = CompareJobStatusCancelled
		j.CancelledAt = &now
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobId":     jobID,
		"status":    string(CompareJobStatusCancelled),
		"cancelled": true,
	})
}

func (s *CompareService) handleDownloadExport(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok, err := s.store.Get(jobID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if job.Status == CompareJobStatusCancelled {
		http.Error(w, "订单已取消", http.StatusGone)
		return
	}
	if !job.Paid || job.Status != CompareJobStatusReady || job.ResultPath == "" {
		http.Error(w, "请先完成支付后再下载结果", http.StatusPaymentRequired)
		return
	}
	if _, err := os.Stat(job.ResultPath); err != nil {
		http.Error(w, "结果文件不存在或已过期", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	// 固定下载文件名：比对结果.xlsx（同时提供 RFC5987 filename* 以兼容 UTF-8）
	utf8Name := "比对结果.xlsx"
	escaped := url.PathEscape(utf8Name)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", "compare.xlsx", escaped))
	http.ServeFile(w, r, job.ResultPath)
}

func newJobID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err == nil {
		return "job_" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("job_%d", time.Now().UnixNano())
}

func safeBaseName(h *multipart.FileHeader) string {
	if h == nil || h.Filename == "" {
		return "upload.xlsx"
	}
	// Prevent path traversal
	return filepath.Base(h.Filename)
}

func saveUploadTo(dir, name string, src multipart.File) (string, error) {
	if dir == "" || name == "" {
		return "", errors.New("invalid path")
	}
	dstPath := filepath.Join(dir, name)
	f, err := os.Create(dstPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return "", err
	}
	return dstPath, nil
}

func (s *CompareService) runCompareTask(jobID string) {
	job, ok, err := s.store.Get(jobID)
	if err != nil {
		return
	}
	if !ok {
		return
	}
	if job.Status == CompareJobStatusCancelled {
		return
	}
	jobDir := filepath.Dir(job.File1Path)
	if jobDir == "" {
		jobDir = filepath.Join(s.tmpRoot, "compare_jobs", jobID)
	}

	// 1) Call Python /compare/export to generate xlsx
	resultPath := filepath.Join(jobDir, "comparison_result.xlsx")
	if err := callPythonCompareExport(s.pyAPIBase, job.File1Path, job.File2Path, resultPath); err != nil {
		_, _, _ = s.store.Update(jobID, func(j *CompareJob) {
			j.Status = CompareJobStatusFailed
			j.Error = err.Error()
		})
		return
	}

	// If cancelled while generating, stop here.
	if j2, ok, _ := s.store.Get(jobID); ok && j2.Status == CompareJobStatusCancelled {
		return
	}

	// If payment already confirmed (rare), directly release.
	if job.Paid {
		_, _, _ = s.store.Update(jobID, func(j *CompareJob) {
			j.Status = CompareJobStatusReady
			j.ResultPath = resultPath
		})
		return
	}

	feeFen := compareJobFeeFen()
	if feeFen <= 0 {
		// Free: no WeChat order, directly mark paid and release result.
		now := time.Now()
		_, _, _ = s.store.Update(jobID, func(j *CompareJob) {
			if j.Status == CompareJobStatusCancelled {
				return
			}
			if !j.Paid {
				j.Paid = true
				j.PaidAt = &now
			}
			j.Status = CompareJobStatusReady
			j.ResultPath = resultPath
			j.AmountYuan = 0
			j.CodeURL = ""
		})
		return
	}

	// 2) Create Native payment (feeFen) and gate result
	codeURL, err := createWechatNativeOrder(jobID, feeFen)
	if err != nil {
		_, _, _ = s.store.Update(jobID, func(j *CompareJob) {
			j.Status = CompareJobStatusFailed
			j.ResultPath = resultPath
			j.Error = "创建微信支付订单失败: " + err.Error()
		})
		return
	}

	_, _, _ = s.store.Update(jobID, func(j *CompareJob) {
		if j.Status == CompareJobStatusCancelled || j.Paid {
			return
		}
		j.Status = CompareJobStatusAwaitingPayment
		j.ResultPath = resultPath
		j.AmountYuan = float64(feeFen) / 100.0
		j.CodeURL = codeURL
	})
}

func callPythonCompareExport(pyBase, file1Path, file2Path, outXLSXPath string) error {
	if pyBase == "" {
		return errors.New("PY_API_BASE 为空")
	}
	if file1Path == "" || file2Path == "" {
		return errors.New("输入文件路径为空")
	}
	if outXLSXPath == "" {
		return errors.New("输出路径为空")
	}

	f1, err := os.Open(file1Path)
	if err != nil {
		return fmt.Errorf("打开 file1 失败: %w", err)
	}
	defer f1.Close()
	f2, err := os.Open(file2Path)
	if err != nil {
		return fmt.Errorf("打开 file2 失败: %w", err)
	}
	defer f2.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := addFilePart(w, "file1", filepath.Base(file1Path), f1); err != nil {
		_ = w.Close()
		return err
	}
	if err := addFilePart(w, "file2", filepath.Base(file2Path), f2); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(pyBase, "/")+"/compare/export", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 Python compare/export 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("Python compare/export 返回错误: %s", msg)
	}

	if err := os.MkdirAll(filepath.Dir(outXLSXPath), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	out, err := os.Create(outXLSXPath)
	if err != nil {
		return fmt.Errorf("创建结果文件失败: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("写入结果文件失败: %w", err)
	}
	return nil
}

func addFilePart(w *multipart.Writer, fieldName, filename string, r io.Reader) error {
	part, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, r); err != nil {
		return err
	}
	return nil
}
