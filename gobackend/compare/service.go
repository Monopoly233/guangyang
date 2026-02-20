package compare

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gobackend/domain"
	"gobackend/queue"
	"gobackend/store"
	"gobackend/wechat"
)

type Service struct {
	store     store.CompareJobStore
	queue     *queue.InMemoryQueue
	tmpRoot   string
	pyAPIBase string
	inflight  chan struct{}
}

var pythonHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
	Timeout: 180 * time.Second,
}

func NewService(st store.CompareJobStore, q *queue.InMemoryQueue, tmpRoot, pyAPIBase string) *Service {
	maxInflight := readEnvIntDefault("COMPARE_MAX_INFLIGHT", 4)
	if maxInflight <= 0 {
		maxInflight = 1
	}
	return &Service{
		store:     st,
		queue:     q,
		tmpRoot:   tmpRoot,
		pyAPIBase: strings.TrimRight(pyAPIBase, "/"),
		inflight:  make(chan struct{}, maxInflight),
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
		} else {
			file2Path = dst
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
	if status == domain.CompareJobStatusAwaitingPayment && job.Paid && job.ResultPath != "" {
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
	if !job.Paid || job.Status != domain.CompareJobStatusReady || job.ResultPath == "" {
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
	if err := callPythonCompareExport(s.pyAPIBase, job.File1Path, job.File2Path, resultPath); err != nil {
		_, _, _ = s.store.Update(jobID, func(j *domain.CompareJob) {
			j.Status = domain.CompareJobStatusFailed
			j.Error = err.Error()
		})
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

	// Stream multipart upload to Python to avoid buffering full request body in memory.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	writeErrCh := make(chan error, 1)
	go func() {
		defer close(writeErrCh)
		defer func() { _ = pw.Close() }()

		if err := addFilePart(mw, "file1", filepath.Base(file1Path), f1); err != nil {
			_ = pw.CloseWithError(err)
			writeErrCh <- err
			return
		}
		if err := addFilePart(mw, "file2", filepath.Base(file2Path), f2); err != nil {
			_ = pw.CloseWithError(err)
			writeErrCh <- err
			return
		}
		if err := mw.Close(); err != nil {
			_ = pw.CloseWithError(err)
			writeErrCh <- err
			return
		}
		writeErrCh <- nil
	}()

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(pyBase, "/")+"/compare/export", pr)
	if err != nil {
		_ = pr.Close()
		return err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := pythonHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 Python compare/export 失败: %w", err)
	}
	defer resp.Body.Close()
	// Ensure the writer goroutine finished without error.
	if werr := <-writeErrCh; werr != nil {
		return werr
	}
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
