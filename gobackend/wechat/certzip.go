package wechat

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

	// WeChat merchant zip contains:
	// - <mchid>_YYYYMMDD_cert/ (dir)
	//   - apiclient_cert.pem (merchant cert)
	//   - apiclient_key.pem (merchant key)
	//   - *_cert.pem (platform cert may exist)
	// - README
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		lower := strings.ToLower(base)

		var outName string
		switch {
		case lower == "apiclient_key.pem" || strings.Contains(lower, "merchant_key"):
			outName = "merchant_key.pem"
		case lower == "apiclient_cert.pem" || strings.Contains(lower, "merchant_cert"):
			outName = "merchant_cert.pem"
		case strings.Contains(lower, "platform") && strings.HasSuffix(lower, ".pem"):
			outName = "platform_cert.pem"
		default:
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		outPath := filepath.Join(destDir, outName)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		out, err := os.Create(outPath)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = out.Close()
			return err
		}
		_ = out.Close()
	}
	return nil
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func findLatestWechatpayCertZip() string {
	re := regexp.MustCompile(`^\d+_\d{8}_cert\.zip$`)
	type c struct {
		path  string
		mtime time.Time
	}
	var cs []c
	for _, dir := range []string{
		filepath.Join("wechatpay", "cert"),
		filepath.Join("..", "wechatpay", "cert"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !re.MatchString(name) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			cs = append(cs, c{path: filepath.Join(dir, name), mtime: info.ModTime()})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].mtime.After(cs[j].mtime) })
	if len(cs) == 0 {
		return ""
	}
	return cs[0].path
}

func findLatestWechatpayAnyZip() string {
	type c struct {
		path  string
		mtime time.Time
	}
	var cs []c
	for _, dir := range []string{
		filepath.Join("wechatpay", "cert"),
		filepath.Join("..", "wechatpay", "cert"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".zip") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			cs = append(cs, c{path: filepath.Join(dir, name), mtime: info.ModTime()})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].mtime.After(cs[j].mtime) })
	if len(cs) == 0 {
		return ""
	}
	return cs[0].path
}
