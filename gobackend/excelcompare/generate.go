package excelcompare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type GenerateOptions struct {
	File1Name string
	File2Name string
	WorkDir   string
}

// GenerateCompareXLSX generates a comparison result workbook at outPath.
// It supports .xlsx directly and .xls via LibreOffice headless conversion.
func GenerateCompareXLSX(outPath, file1Path, file2Path string, opt GenerateOptions) error {
	file1Path = strings.TrimSpace(file1Path)
	file2Path = strings.TrimSpace(file2Path)
	if file1Path == "" || file2Path == "" {
		return errors.New("输入文件路径为空")
	}

	workDir := strings.TrimSpace(opt.WorkDir)
	if workDir == "" {
		workDir = filepath.Dir(outPath)
	}
	if workDir == "" {
		workDir = "."
	}

	x1, err := ensureXLSX(workDir, file1Path)
	if err != nil {
		return fmt.Errorf("转换 file1 失败: %w", err)
	}
	x2, err := ensureXLSX(workDir, file2Path)
	if err != nil {
		return fmt.Errorf("转换 file2 失败: %w", err)
	}

	t1, err := ReadFirstSheetXLSX(x1)
	if err != nil {
		return err
	}
	t2, err := ReadFirstSheetXLSX(x2)
	if err != nil {
		return err
	}

	keyCol, err := GuessPrimaryKeyColumn(t1.Headers)
	if err != nil {
		return err
	}
	res, err := CompareTables(t1, t2, keyCol)
	if err != nil {
		return err
	}
	return ExportResultXLSX(outPath, res, ExportOptions{File1Name: opt.File1Name, File2Name: opt.File2Name})
}

func ensureXLSX(workDir, inPath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(inPath)))
	if ext != ".xls" {
		return inPath, nil
	}

	// Prefer remote converter microservice to avoid shipping LibreOffice in Go main container.
	if convBase := strings.TrimSpace(os.Getenv("XLS_CONVERT_BASE")); convBase != "" {
		outPath := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), ext)+".xlsx")
		if err := convertViaHTTP(convBase, inPath, outPath); err != nil {
			return "", err
		}
		return outPath, nil
	}

	// Fallback: local soffice (for local dev).
	return convertViaSoffice(workDir, inPath)
}

func convertViaSoffice(workDir, inPath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(inPath)))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		"soffice",
		"--headless",
		"--nologo",
		"--nolockcheck",
		"--norestore",
		"--convert-to", "xlsx",
		"--outdir", workDir,
		inPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("soffice 转换失败: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	base := strings.TrimSuffix(filepath.Base(inPath), ext)
	return filepath.Join(workDir, base+".xlsx"), nil
}

func convertViaHTTP(baseURL, inPath, outPath string) error {
	// POST {baseURL}/convert  multipart: file=@inPath  -> response body is xlsx bytes
	u := strings.TrimRight(baseURL, "/")
	u = u + path.Clean("/convert")

	f, err := os.Open(inPath)
	if err != nil {
		return fmt.Errorf("打开 xls 失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	writeErrCh := make(chan error, 1)
	go func() {
		defer close(writeErrCh)
		defer func() { _ = pw.Close() }()
		part, err := mw.CreateFormFile("file", filepath.Base(inPath))
		if err != nil {
			_ = pw.CloseWithError(err)
			writeErrCh <- err
			return
		}
		if _, err := io.Copy(part, f); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")

	client := &http.Client{Timeout: 130 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 xls 转换服务失败: %w", err)
	}
	defer resp.Body.Close()
	if werr := <-writeErrCh; werr != nil {
		return werr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("xls 转换服务返回错误: %s", msg)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("写入转换结果失败: %w", err)
	}
	return nil
}


