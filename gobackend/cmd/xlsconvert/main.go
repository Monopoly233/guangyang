package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// Start a persistent LibreOffice listener via unoserver to avoid per-request cold start.
	// This keeps LibreOffice warm in the container.
	if err := startUnoserver("127.0.0.1", "2003", "127.0.0.1", "2002"); err != nil {
		log.Fatalf("failed to start unoserver: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/convert", handleConvert)

	addr := ":" + readEnvDefault("PORT", "8090")
	log.Printf("xlsconvert listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	// Healthy only if listener port is accepting connections.
	if err := waitTCP("127.0.0.1:2003", 200*time.Millisecond, 1); err != nil {
		http.Error(w, "listener not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Stream to disk.
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid multipart", http.StatusBadRequest)
		return
	}

	tmpRoot := strings.TrimSpace(readEnvDefault("TMP_ROOT", "/tmp"))
	jobDir, err := os.MkdirTemp(tmpRoot, "xlsconvert-*")
	if err != nil {
		http.Error(w, "failed to create temp dir", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(jobDir)

	var inPath string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			http.Error(w, "read multipart failed", http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		name := safeBase(part.FileName())
		if name == "" {
			name = "input.xls"
		}
		inPath = filepath.Join(jobDir, name)
		out, err := os.Create(inPath)
		if err != nil {
			_ = part.Close()
			http.Error(w, "create temp file failed", http.StatusInternalServerError)
			return
		}
		_, cpErr := io.Copy(out, part)
		_ = out.Close()
		_ = part.Close()
		if cpErr != nil {
			http.Error(w, "save upload failed", http.StatusBadRequest)
			return
		}
		break
	}
	if strings.TrimSpace(inPath) == "" {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(inPath))
	if ext != ".xls" && ext != ".xlsx" {
		http.Error(w, "only .xls/.xlsx supported", http.StatusBadRequest)
		return
	}

	// If already xlsx, just return.
	if ext == ".xlsx" {
		f, err := os.Open(inPath)
		if err != nil {
			http.Error(w, "open failed", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		_, _ = io.Copy(w, f)
		return
	}

	outPath := filepath.Join(jobDir, strings.TrimSuffix(filepath.Base(inPath), ".xls")+".xlsx")
	if err := convertWithListener(r.Context(), inPath, outPath); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := os.Open(outPath)
	if err != nil {
		http.Error(w, "open converted file failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_, _ = io.Copy(w, f)
}

func safeBase(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return filepath.Base(name)
}

func readEnvDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func startUnoserver(interfaceHost, port, unoInterface, unoPort string) error {
	interfaceHost = strings.TrimSpace(interfaceHost)
	port = strings.TrimSpace(port)
	unoInterface = strings.TrimSpace(unoInterface)
	unoPort = strings.TrimSpace(unoPort)
	if interfaceHost == "" {
		interfaceHost = "127.0.0.1"
	}
	if port == "" {
		port = "2003"
	}
	if unoInterface == "" {
		unoInterface = "127.0.0.1"
	}
	if unoPort == "" {
		unoPort = "2002"
	}

	cmd := exec.Command(
		"unoserver",
		"--interface", interfaceHost,
		"--port", port,
		"--uno-interface", unoInterface,
		"--uno-port", unoPort,
		"--conversion-timeout", "120",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	// Wait until listener socket is ready.
	if err := waitTCP(net.JoinHostPort(interfaceHost, port), 500*time.Millisecond, 90); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("listener port not ready: %w", err)
	}

	// Reap child in background.
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("unoconv listener exited: %v", err)
		} else {
			log.Printf("unoconv listener exited")
		}
	}()
	return nil
}

func waitTCP(addr string, interval time.Duration, tries int) error {
	var last error
	for i := 0; i < tries; i++ {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		last = err
		time.Sleep(interval)
	}
	if last == nil {
		last = errors.New("unknown dial error")
	}
	return last
}

func convertWithListener(ctx context.Context, inPath, outPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// Ensure listener is up; if not, fail fast (do not auto-launch per request).
	if err := waitTCP("127.0.0.1:2003", 200*time.Millisecond, 5); err != nil {
		return fmt.Errorf("listener 不可用: %w", err)
	}

	// Use unoconvert client to connect to existing listener, no cold-start.
	cmd := exec.CommandContext(ctx,
		"unoconvert",
		"--host", "127.0.0.1",
		"--port", "2003",
		"--host-location", "local",
		"--convert-to", "xlsx",
		inPath,
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unoconvert 转换失败: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

