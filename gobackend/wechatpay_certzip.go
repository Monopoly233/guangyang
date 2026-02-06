package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func wechatpayTmpRoot() string {
	if v := strings.TrimSpace(os.Getenv("TMP_ROOT")); v != "" {
		return v
	}
	return "./tmp"
}

func wechatpayCertCacheDir() string {
	return filepath.Join(wechatpayTmpRoot(), "wechatpay_cache", "cert")
}

// ensureWechatpayMerchantPemFiles tries to ensure merchant_key.pem / merchant_cert.pem exist.
// It supports two modes:
// - user provides pem files under wechatpay/cert/
// - user only provides <mchid>_<yyyymmdd>_cert.zip under wechatpay/cert/, we will extract into tmp cache
func ensureWechatpayMerchantPemFiles() (merchantKeyPath, merchantCertPath, platformCertPath string, err error) {
	// 1) try direct paths (mounted secrets)
	direct := [][]string{
		{filepath.Join("wechatpay", "cert", "merchant_key.pem"), filepath.Join("wechatpay", "cert", "merchant_cert.pem"), filepath.Join("wechatpay", "cert", "platform_cert.pem")},
		{filepath.Join("..", "wechatpay", "cert", "merchant_key.pem"), filepath.Join("..", "wechatpay", "cert", "merchant_cert.pem"), filepath.Join("..", "wechatpay", "cert", "platform_cert.pem")},
	}
	for _, c := range direct {
		if fileExists(c[0]) && fileExists(c[1]) {
			// platform cert is optional now
			if fileExists(c[2]) {
				return c[0], c[1], c[2], nil
			}
			return c[0], c[1], "", nil
		}
	}

	// 2) try cached extracted paths
	cacheDir := wechatpayCertCacheDir()
	cacheKey := filepath.Join(cacheDir, "merchant_key.pem")
	cacheCert := filepath.Join(cacheDir, "merchant_cert.pem")
	cachePlatform := filepath.Join(cacheDir, "platform_cert.pem")
	if fileExists(cacheKey) && fileExists(cacheCert) {
		if fileExists(cachePlatform) {
			return cacheKey, cacheCert, cachePlatform, nil
		}
		return cacheKey, cacheCert, "", nil
	}

	// 3) extract from latest cert.zip into cache dir (writable even when wechatpay/ is mounted ro)
	zipPath := findLatestWechatpayCertZip()
	if zipPath == "" {
		return "", "", "", errors.New("缺少商户证书文件：请在 wechatpay/cert/ 下放置 merchant_key.pem、merchant_cert.pem，或放置 <mchid>_YYYYMMDD_cert.zip 以便自动解压")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("创建证书缓存目录失败: %w", err)
	}
	if err := extractWechatpayCertZip(zipPath, cacheDir); err != nil {
		return "", "", "", err
	}
	if !fileExists(cacheKey) || !fileExists(cacheCert) {
		return "", "", "", fmt.Errorf("已解压 cert.zip 但仍缺少 merchant_key.pem/merchant_cert.pem（zip=%s）", zipPath)
	}
	if fileExists(cachePlatform) {
		return cacheKey, cacheCert, cachePlatform, nil
	}
	return cacheKey, cacheCert, "", nil
}

func extractWechatpayCertZip(zipPath string, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 cert.zip 失败: %w", err)
	}
	defer r.Close()

	// Extract pem-like files into destDir (flat), then normalize filenames to our expected ones.
	extracted := map[string]string{} // base -> fullpath

	for _, f := range r.File {
		if f == nil || f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		lower := strings.ToLower(base)
		if !strings.HasSuffix(lower, ".pem") && !strings.HasSuffix(lower, ".p12") {
			continue
		}
		// Prevent path traversal by flattening to base name
		outPath := filepath.Join(destDir, base)
		if err := extractZipFileTo(f, outPath); err != nil {
			return fmt.Errorf("解压 %s 失败: %w", base, err)
		}
		extracted[base] = outPath
	}

	// Prefer known filenames from WeChat merchant cert package
	// - apiclient_key.pem (merchant private key)
	// - apiclient_cert.pem (merchant cert)
	// Platform cert/public-key names vary; we try best-effort mapping.
	var (
		srcKey      = pickFirstExisting(extracted, []string{"apiclient_key.pem", "merchant_key.pem"})
		srcCert     = pickFirstExisting(extracted, []string{"apiclient_cert.pem", "merchant_cert.pem"})
		srcPlatformCert = pickFirstExistingByContains(extracted, []string{"platform", "cert"}, ".pem")
		srcPlatformKey  = pickFirstExistingByContains(extracted, []string{"platform", "key"}, ".pem")
	)

	// Some packages include only .p12 for merchant cert; we cannot convert without password/openssl.
	if srcKey == "" {
		srcKey = pickFirstExistingByContains(extracted, []string{"key"}, ".pem")
	}
	if srcCert == "" {
		srcCert = pickFirstExistingByContains(extracted, []string{"cert"}, ".pem")
	}
	// Platform public key may be named like "platform_public_key.pem" (no "cert" token).
	if srcPlatformKey == "" {
		srcPlatformKey = pickFirstExistingByContains(extracted, []string{"platform", "public"}, ".pem")
	}
	if srcPlatformKey == "" {
		srcPlatformKey = pickFirstExistingByContains(extracted, []string{"public", "key"}, ".pem")
	}

	if srcKey != "" {
		if err := copyFile(srcKey, filepath.Join(destDir, "merchant_key.pem")); err != nil {
			return err
		}
	}
	if srcCert != "" {
		if err := copyFile(srcCert, filepath.Join(destDir, "merchant_cert.pem")); err != nil {
			return err
		}
	}
	if srcPlatformCert != "" {
		_ = copyFile(srcPlatformCert, filepath.Join(destDir, "platform_cert.pem"))
	}
	if srcPlatformKey != "" {
		_ = copyFile(srcPlatformKey, filepath.Join(destDir, "platform_public_key.pem"))
	}
	return nil
}

func extractZipFileTo(f *zip.File, outPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp := outPath + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, outPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func pickFirstExisting(m map[string]string, names []string) string {
	for _, n := range names {
		if p, ok := m[n]; ok && p != "" {
			return p
		}
	}
	return ""
}

func pickFirstExistingByContains(m map[string]string, tokens []string, suffix string) string {
	type cand struct {
		base string
		path string
	}
	var cands []cand
	for base, p := range m {
		lb := strings.ToLower(base)
		if suffix != "" && !strings.HasSuffix(lb, strings.ToLower(suffix)) {
			continue
		}
		ok := true
		for _, t := range tokens {
			if !strings.Contains(lb, strings.ToLower(t)) {
				ok = false
				break
			}
		}
		if ok {
			cands = append(cands, cand{base: base, path: p})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].base < cands[j].base })
	return cands[0].path
}

func findLatestWechatpayCertZip() string {
	// <mchid>_<yyyymmdd>_cert.zip
	re := regexp.MustCompile(`^(\d+)_\d{8}_cert\.zip$`)
	type candidate struct {
		path  string
		mtime time.Time
	}
	var cands []candidate
	dirs := []string{
		filepath.Join("wechatpay", "cert"),
		filepath.Join("..", "wechatpay", "cert"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if !re.MatchString(name) {
				continue
			}
			info, err := ent.Info()
			if err != nil {
				continue
			}
			cands = append(cands, candidate{
				path:  filepath.Join(dir, name),
				mtime: info.ModTime(),
			})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	return cands[0].path
}

// findLatestWechatpayAnyZip returns the newest .zip under wechatpay/cert/.
// This is used as a fallback for platform verification materials, since platform files
// may come in a differently named zip than <mchid>_YYYYMMDD_cert.zip.
func findLatestWechatpayAnyZip() string {
	type candidate struct {
		path  string
		mtime time.Time
	}
	var cands []candidate
	dirs := []string{
		filepath.Join("wechatpay", "cert"),
		filepath.Join("..", "wechatpay", "cert"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			name := strings.ToLower(ent.Name())
			if !strings.HasSuffix(name, ".zip") {
				continue
			}
			info, err := ent.Info()
			if err != nil {
				continue
			}
			cands = append(cands, candidate{
				path:  filepath.Join(dir, ent.Name()),
				mtime: info.ModTime(),
			})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	return cands[0].path
}

