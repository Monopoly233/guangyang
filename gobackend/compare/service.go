package compare

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	"time"

	"gobackend/domain"
	"gobackend/excelcompare"
	"gobackend/ossstore"
	"gobackend/queue"
	"gobackend/store"
	"gobackend/wechat"
)

type Service struct {
	store     store.CompareJobStore
	queue     *queue.InMemoryQueue
	tmpRoot   string
	inflight  chan struct{}
	oss       *ossstore.Store
}

func NewService(st store.CompareJobStore, q *queue.InMemoryQueue, tmpRoot string, oss *ossstore.Store) *Service {
	maxInflight := readEnvIntDefault("COMPARE_MAX_INFLIGHT", 4)
	if maxInflight <= 0 {
		maxInflight = 1
	}
	return &Service{
		store:     st,
		queue:     q,
		tmpRoot:   tmpRoot,
		inflight:  make(chan struct{}, maxInflight),
		oss:       oss,
	}
}

// FeeFen returns fee in "fen" (1 yuan = 100 fen).
// Default is 0 (free). You can override by setting env COMPARE_JOB_FEE_FEN (non-negative integer).
func FeeFen() int64 {
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

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/compare/jobs", s.handleCreateJob)
	mux.HandleFunc("/compare/jobs/", s.handleJobRoutes)
}

func (s *Service) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Stream multipart to disk to reduce memory usage (avoid ParseMultipartForm buffering).
	maxUploadMB := readEnvIntDefault("COMPARE_MAX_UPLOAD_MB", 128)
	if maxUploadMB <= 0 {
		maxUploadMB = 128
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxUploadMB)<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	jobID := newJobID()
	jobDir := filepath.Join(s.tmpRoot, "compare_jobs", jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		http.Error(w, "failed to create job dir", http.StatusInternalServerError)
		return
	}

	var (
		file1Path string
		file2Path string
		file1Name string
		file2Name string
	)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "invalid multipart stream", http.StatusBadRequest)
			return
		}
		if part == nil {
			continue
		}
		name := strings.TrimSpace(part.FormName())
		if name != "file1" && name != "file2" {
			// Drain unknown parts to keep parser healthy.
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			continue
		}

		fn := safeBaseNameFromName(part.FileName())
		prefix := "file1_"
		if name == "file2" {
			prefix = "file2_"
		}
		dst, err := saveUploadTo(jobDir, prefix+fn, part)
		_ = part.Close()
		if err != nil {
			http.Error(w, "failed to save "+name, http.StatusInternalServerError)
			return
		}
		if name == "file1" {
			file1Path = dst
			file1Name = fn
		} else {
			file2Path = dst
			file2Name = fn
		}
	}
	if file1Path == "" || file2Path == "" {
		http.Error(w, "missing file1 or file2", http.StatusBadRequest)
		return
	}

	job := &domain.CompareJob{
		ID:        jobID,
		Status:    domain.CompareJobStatusProcessing,
		CreatedAt: time.Now(),
		File1Path: file1Path,
		File2Path: file2Path,
		File1Name: file1Name,
		File2Name: file2Name,
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

func (s *Service) handleJobRoutes(w http.ResponseWriter, r *http.Request) {
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

func (s *Service) handleGetJob(w http.ResponseWriter, r *http.Request, jobID string) {
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
	if status == domain.CompareJobStatusAwaitingPayment && job.Paid && hasResult(job) {
		status = domain.CompareJobStatusReady
	}
	// Return a safe subset
	resp := map[string]interface{}{
		"jobId":     job.ID,
		"status":    string(status),
		"createdAt": job.CreatedAt,
		"paid":      job.Paid,
	}
	if status == domain.CompareJobStatusAwaitingPayment {
		resp["amount"] = job.AmountYuan
		resp["code_url"] = job.CodeURL
	}
	if job.Status == domain.CompareJobStatusFailed && job.Error != "" {
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

func (s *Service) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
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
	if job.Status == domain.CompareJobStatusCancelled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"jobId":     job.ID,
			"status":    string(job.Status),
			"cancelled": true,
		})
		return
	}

	// If already paid/released, don't allow cancel.
	if job.Paid || job.Status == domain.CompareJobStatusReady {
		http.Error(w, "订单已支付或已放行，无法取消", http.StatusConflict)
		return
	}

	// If we already created a WeChat order, attempt to close it first.
	if job.Status == domain.CompareJobStatusAwaitingPayment {
		if err := wechat.CloseNativeOrder(jobID); err != nil {
			http.Error(w, "关闭微信订单失败: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	now := time.Now()
	_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
		// Don't overwrite if paid concurrently.
		if j.Paid {
			return
		}
		j.Status = domain.CompareJobStatusCancelled
		j.CancelledAt = &now
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobId":     jobID,
		"status":    string(domain.CompareJobStatusCancelled),
		"cancelled": true,
	})
}

func (s *Service) handleDownloadExport(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok, err := s.store.Get(jobID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if job.Status == domain.CompareJobStatusCancelled {
		http.Error(w, "订单已取消", http.StatusGone)
		return
	}
	if !job.Paid || job.Status != domain.CompareJobStatusReady || !hasResult(job) {
		http.Error(w, "请先完成支付后再下载结果", http.StatusPaymentRequired)
		return
	}
	// Prefer OSS signed URL when available (cross-pod safe).
	if job.ResultOSSKey != "" && s.oss != nil && s.oss.Enabled() {
		signed, err := s.oss.SignDownloadURL(job.ResultOSSKey, "比对结果.xlsx")
		if err != nil {
			http.Error(w, "生成下载链接失败", http.StatusBadGateway)
			return
		}
		// 支持两种响应：
		// - format=json：返回 {url, filename} 让前端自行 fetch(预览)/跳转(下载)
		// - 默认：302 重定向到 OSS 签名链接（适合纯下载）
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"url":      signed,
				"filename": "比对结果.xlsx",
			})
			return
		}
		http.Redirect(w, r, signed, http.StatusFound)
		return
	}

	// Fallback: local filesystem
	if job.ResultPath == "" {
		http.Error(w, "结果文件不存在或已过期", http.StatusGone)
		return
	}
	if _, err := os.Stat(job.ResultPath); err != nil {
		http.Error(w, "结果文件不存在或已过期", http.StatusGone)
		return
	}
	if wantsJSON(r) {
		// 返回相对路径，让前端用 GO_API_BASE 拼接（兼容 Nginx /api 前缀代理）
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"url":      r.URL.Path,
			"filename": "比对结果.xlsx",
		})
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	// 固定下载文件名：比对结果.xlsx（同时提供 RFC5987 filename* 以兼容 UTF-8）
	utf8Name := "比对结果.xlsx"
	escaped := url.PathEscape(utf8Name)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", "compare.xlsx", escaped))
	http.ServeFile(w, r, job.ResultPath)
}

func wantsJSON(r *http.Request) bool {
	if r == nil {
		return false
	}
	q := r.URL.Query()
	if strings.EqualFold(strings.TrimSpace(q.Get("format")), "json") {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "application/json")
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

func saveUploadTo(dir, name string, src io.Reader) (string, error) {
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

func (s *Service) runCompareTask(jobID string) {
	// Backpressure: limit concurrent compare executions per pod.
	s.acquireInflight()
	defer s.releaseInflight()

	job, ok, err := s.store.Get(jobID)
	if err != nil {
		return
	}
	if !ok {
		return
	}
	if job.Status == domain.CompareJobStatusCancelled {
		return
	}
	jobDir := filepath.Dir(job.File1Path)
	if jobDir == "" {
		jobDir = filepath.Join(s.tmpRoot, "compare_jobs", jobID)
	}

	// 1) Call Python /compare/export to generate xlsx
	resultPath := filepath.Join(jobDir, "comparison_result.xlsx")
	if err := excelcompare.GenerateCompareXLSX(
		resultPath,
		job.File1Path,
		job.File2Path,
		excelcompare.GenerateOptions{
			File1Name: job.File1Name,
			File2Name: job.File2Name,
			WorkDir:   jobDir,
		},
	); err != nil {
		_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
			j.Status = domain.CompareJobStatusFailed
			j.Error = err.Error()
		})
		return
	}

	// 1.5) Upload result to OSS (if enabled) for cross-pod download.
	var ossKey string
	if s.oss != nil && s.oss.Enabled() {
		ossKey = s.oss.ObjectKeyForJob(jobID)
		if err := s.oss.PutResultFile(ossKey, resultPath); err != nil {
			_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
				j.Status = domain.CompareJobStatusFailed
				j.Error = "上传 OSS 失败: " + err.Error()
			})
			return
		}
		// Best-effort cleanup: local file is no longer needed once uploaded.
		_ = os.Remove(resultPath)
	}

	// Persist result location early to avoid races with WeChat notify / polling.
	_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
		if j.Status == domain.CompareJobStatusCancelled {
			return
		}
		if ossKey != "" {
			j.ResultOSSKey = ossKey
			j.ResultPath = ""
			return
		}
		j.ResultPath = resultPath
	})

	// Refresh job state after generating result (Paid/Cancelled may change concurrently).
	job, ok, err = s.store.Get(jobID)
	if err != nil || !ok {
		return
	}

	// If cancelled while generating, stop here.
	if j2, ok, _ := s.store.Get(jobID); ok && j2.Status == domain.CompareJobStatusCancelled {
		return
	}

	// If payment already confirmed (rare), directly release.
	if job.Paid {
		_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
			j.Status = domain.CompareJobStatusReady
			j.ResultPath = resultPath
			if ossKey != "" {
				j.ResultOSSKey = ossKey
				j.ResultPath = ""
			}
		})
		return
	}

	feeFen := FeeFen()
	if feeFen <= 0 {
		// Free: no WeChat order, directly mark paid and release result.
		now := time.Now()
		_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
			if j.Status == domain.CompareJobStatusCancelled {
				return
			}
			if !j.Paid {
				j.Paid = true
				j.PaidAt = &now
			}
			j.Status = domain.CompareJobStatusReady
			j.ResultPath = resultPath
			if ossKey != "" {
				j.ResultOSSKey = ossKey
				j.ResultPath = ""
			}
			j.AmountYuan = 0
			j.CodeURL = ""
		})
		return
	}

	// 2) Create Native payment (feeFen) and gate result
	codeURL, err := wechat.CreateNativeOrder(jobID, feeFen)
	if err != nil {
		_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
			j.Status = domain.CompareJobStatusFailed
			j.ResultPath = resultPath
			if ossKey != "" {
				j.ResultOSSKey = ossKey
				j.ResultPath = ""
			}
			j.Error = "创建微信支付订单失败: " + err.Error()
		})
		return
	}

	_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
		if j.Status == domain.CompareJobStatusCancelled || j.Paid {
			return
		}
		j.Status = domain.CompareJobStatusAwaitingPayment
		j.ResultPath = resultPath
		if ossKey != "" {
			j.ResultOSSKey = ossKey
			j.ResultPath = ""
		}
		j.AmountYuan = float64(feeFen) / 100.0
		j.CodeURL = codeURL
	})
}

func hasResult(job *domain.CompareJob) bool {
	if job == nil {
		return false
	}
	return strings.TrimSpace(job.ResultOSSKey) != "" || strings.TrimSpace(job.ResultPath) != ""
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Service) acquireInflight() {
	if s.inflight == nil {
		return
	}
	s.inflight <- struct{}{}
}

func (s *Service) releaseInflight() {
	if s.inflight == nil {
		return
	}
	select {
	case <-s.inflight:
	default:
	}
}

func safeBaseNameFromName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "upload.xlsx"
	}
	return filepath.Base(name)
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
